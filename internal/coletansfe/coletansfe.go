// Package coletansfe coleta NFSe (recebida, emitida, cancelamento) via
// Sistema Nacional NFS-e (ADN) — webservice oficial gratuito da SEFAZ.
package coletansfe

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"io-nf-automation/internal/adn"
	"io-nf-automation/internal/appconfig"
	"io-nf-automation/internal/catalogo"
	"io-nf-automation/internal/document"
	"io-nf-automation/internal/nfedist"
	"io-nf-automation/internal/organizer"
)

// MaxPaginas — cautela deliberada, mesmo espírito do coletanfe.
const MaxPaginas = 12

// Resumo é o resultado estruturado de uma rodada de Run() — mesmo espírito
// do coletanfe.Resumo, pro painel conseguir dizer "0 novas porque já está
// em dia" vs "0 novas porque as páginas desta rodada acabaram antes de
// achar algo novo".
type Resumo struct {
	Paginas               int
	Recebidas             int
	Emitidas              int
	Cancelamentos         int
	SemReferencia         int
	Outras                int
	OutrosTipos           int
	Erros                 int
	NSU                   int
	StatusProcessamento   string
	TipoAmbiente          string
	Alertas               []string
	ErrosAPI              []string
	TiposDocumento        map[string]int
	ParouPorLimitePaginas bool // rodou até MaxPaginas sem esvaziar — pode ter mais
}

func Run(cfg appconfig.Config) (Resumo, error) {
	resumo := Resumo{TiposDocumento: make(map[string]int)}
	client, err := adn.NewClient(cfg.CertificadoPfx, cfg.CertificadoSenha, cfg.TpAmb())
	if err != nil {
		return resumo, fmt.Errorf("criar client ADN: %w", err)
	}

	// Raiz separada por direção — NFE (entrada) recebe serviço tomado pela
	// empresa, NFS (saída) recebe serviço prestado por ela. Antes as duas
	// ficavam juntas em "nfse/", só diferenciadas pelo Tipo — a pasta raiz
	// agora deixa a separação física, batendo com a convenção usada na
	// pasta de arquivo real (Y:\...\NF ENTRADA\ / NF SAÍDA\).
	outDirRecebida := filepath.Join(cfg.PastaEfetiva(), "NFE", "NFE_SERVICO")
	outDirEmitida := filepath.Join(cfg.PastaEfetiva(), "NFS", "NFS_SERVICO")
	pastaControle := appconfig.PastaControle(cfg.PastaEfetiva())
	if err := os.MkdirAll(pastaControle, 0o755); err != nil {
		return resumo, fmt.Errorf("criar pasta _Controle: %w", err)
	}
	checkpointPath := filepath.Join(pastaControle, ".checkpoint-adn-nsu")

	nsu, err := nfedist.LerCheckpoint(checkpointPath)
	if err != nil {
		return resumo, fmt.Errorf("ler checkpoint NFSe: %w", err)
	}
	fmt.Printf("[NFSe] Retomando a partir do NSU %d\n", nsu)

	var recebidas, emitidas, outras, erros, cancelamentos, semReferencia, outrosTipos int

	type registro struct {
		caminho string
		doc     document.Document
	}
	processadas := make(map[string]registro)

	// Mesma razão do coletanfe: sem pré-carregar o que já existe, reprocessar
	// o mesmo NSU duplica arquivo (gravando de novo, colidindo e criando
	// cópia com sufixo), e cancelamento de nota de rodada anterior não
	// encontra a nota original.
	if existentes, err := catalogo.Listar(cfg.PastaEfetiva()); err != nil {
		log.Printf("[NFSe] aviso: não consegui pré-carregar o catálogo (seguindo sem isso): %v", err)
	} else {
		for _, ex := range existentes {
			if ex.Chave == "" || ex.Caminho == "" {
				continue
			}
			if _, statErr := os.Stat(ex.Caminho); statErr != nil {
				continue
			}
			processadas[ex.Chave] = registro{
				caminho: ex.Caminho,
				doc: document.Document{
					Tipo: document.Tipo(ex.Tipo), Fornecedor: ex.Fornecedor,
					FornecedorDoc: ex.FornecedorDoc, Data: ex.Data, Numero: ex.Numero,
					Valor: ex.Valor, Status: ex.Status, Chave: ex.Chave,
				},
			}
		}
	}

	resumo.ParouPorLimitePaginas = true
	for pagina := 0; pagina < MaxPaginas; pagina++ {
		resp, err := client.BuscarLote(nsu)
		if err != nil {
			log.Printf("[NFSe] buscar lote NSU=%d: %v — parando", nsu, err)
			erros++
			resumo.Erros = erros
			resumo.ParouPorLimitePaginas = false
			return resumo, fmt.Errorf("buscar lote NSU=%d: %w", nsu, err)
		}
		resumo.Paginas++
		resumo.StatusProcessamento = resp.StatusProcessamento
		resumo.TipoAmbiente = resp.TipoAmbiente
		resumo.Alertas = append(resumo.Alertas, resp.Alertas...)
		resumo.ErrosAPI = append(resumo.ErrosAPI, resp.Erros...)

		fmt.Printf("[NFSe] Página %d — NSU inicial %d — ambiente: %s — status: %s — %d documentos\n",
			pagina+1, nsu, resp.TipoAmbiente, resp.StatusProcessamento, len(resp.LoteDFe))
		if len(resp.Alertas) > 0 {
			log.Printf("[NFSe] alertas ADN: %s", strings.Join(resp.Alertas, " | "))
		}
		if len(resp.Erros) > 0 {
			log.Printf("[NFSe] erros ADN: %s", strings.Join(resp.Erros, " | "))
			erros += len(resp.Erros)
			resumo.Erros = erros
			if len(resp.LoteDFe) == 0 {
				resumo.ParouPorLimitePaginas = false
				return resumo, fmt.Errorf("ADN retornou erro sem documentos: %s", strings.Join(resp.Erros, " | "))
			}
		}

		if len(resp.LoteDFe) == 0 {
			resumo.ParouPorLimitePaginas = false
			break
		}

		maiorNSU := nsu
		for _, item := range resp.LoteDFe {
			if item.NSU > maiorNSU {
				maiorNSU = item.NSU
			}

			xmlBytes, err := adn.DecodeXML(item.ArquivoXml)
			if err != nil {
				log.Printf("[NFSe]   NSU %d: erro ao decodificar: %v", item.NSU, err)
				erros++
				continue
			}

			tipoDocumento := tipoDocumentoADN(item.TipoDocumento)
			tipoMapa := strings.TrimSpace(item.TipoDocumento)
			if tipoMapa == "" {
				tipoMapa = "(vazio)"
			}
			resumo.TiposDocumento[tipoMapa]++

			if tipoDocumento == "EVENTO" {
				ev, err := document.ParseEvento(xmlBytes)
				if err != nil {
					log.Printf("[NFSe]   NSU %d: erro ao parsear evento: %v", item.NSU, err)
					erros++
					continue
				}

				reg, achou := processadas[ev.ChaveOriginal]
				if achou && reg.doc.Status == "CANCELADO" {
					continue
				}
				if achou {
					novoCaminho, err := organizer.MarcarCancelado(reg.caminho, reg.doc)
					if err != nil {
						log.Printf("[NFSe]   NSU %d: erro ao marcar cancelado: %v", item.NSU, err)
						erros++
						continue
					}
					reg.caminho = novoCaminho
					reg.doc.Status = "CANCELADO"
					processadas[ev.ChaveOriginal] = reg
					if err := catalogo.Registrar(cfg.PastaEfetiva(), reg.doc, reg.caminho); err != nil {
						log.Printf("[NFSe]   NSU %d: aviso ao registrar cancelamento no catálogo: %v", item.NSU, err)
					}
					cancelamentos++
					fmt.Printf("[NFSe]   NSU %d [CANCELAMENTO] renomeado -> %s\n", item.NSU, novoCaminho)
				} else {
					// evento órfão sem a nota original nesta rodada — não dá pra
					// saber se era recebida ou emitida, grava do lado da entrada
					// por padrão (mesmo comportamento de sempre, só mudou a raiz).
					caminho, err := organizer.PlaceEventoSemReferencia(outDirRecebida, ev, xmlBytes)
					if err != nil {
						log.Printf("[NFSe]   NSU %d: erro ao gravar evento sem referência: %v", item.NSU, err)
						erros++
						continue
					}
					semReferencia++
					fmt.Printf("[NFSe]   NSU %d [CANCELAMENTO sem nota nesta rodada] -> %s\n", item.NSU, caminho)
				}
				continue
			}

			if tipoDocumento != "NFSE" {
				log.Printf("[NFSe]   NSU %d: tipo %q não é NFSe — ignorando", item.NSU, item.TipoDocumento)
				outrosTipos++
				continue
			}

			doc, direcao, err := document.ParseNFSeNacional(xmlBytes, cfg.CNPJ)
			if err != nil {
				log.Printf("[NFSe]   NSU %d: erro ao parsear XML: %v", item.NSU, err)
				erros++
				continue
			}
			doc.Chave = item.ChaveAcesso

			outDir := outDirRecebida
			switch direcao {
			case document.DirecaoRecebida:
				recebidas++
			case document.DirecaoEmitida:
				emitidas++
				doc.Tipo = document.TipoNFESEmitida
				outDir = outDirEmitida
			default:
				log.Printf("[NFSe]   NSU %d: CNPJ do XML não bate com o CNPJ configurado — ignorando chave %s", item.NSU, item.ChaveAcesso)
				outras++
				continue
			}

			if reg, ja := processadas[doc.Chave]; ja && reg.doc.Status == doc.Status {
				fmt.Printf("[NFSe]   NSU %d [%s] já existe (%s) -> pulando\n", item.NSU, direcao, reg.caminho)
				continue
			}
			caminho, err := organizer.PlaceDocumentPlano(outDir, doc, ".xml", xmlBytes)
			if err != nil {
				log.Printf("[NFSe]   NSU %d: erro ao gravar: %v", item.NSU, err)
				erros++
				continue
			}
			processadas[doc.Chave] = registro{caminho: caminho, doc: doc}
			if err := catalogo.Registrar(cfg.PastaEfetiva(), doc, caminho); err != nil {
				log.Printf("[NFSe]   NSU %d: aviso ao registrar no catálogo: %v", item.NSU, err)
			}
			fmt.Printf("[NFSe]   NSU %d [%s] -> %s\n", item.NSU, direcao, caminho)
		}

		nsu = maiorNSU
		if err := nfedist.SalvarCheckpoint(checkpointPath, nsu); err != nil {
			log.Printf("[NFSe] erro ao salvar checkpoint: %v", err)
		}
		time.Sleep(2 * time.Second)
	}

	resumo.Recebidas = recebidas
	resumo.Emitidas = emitidas
	resumo.Cancelamentos = cancelamentos
	resumo.SemReferencia = semReferencia
	resumo.Outras = outras
	resumo.OutrosTipos = outrosTipos
	resumo.Erros = erros
	resumo.NSU = nsu

	fmt.Println("[NFSe] === Resumo ===")
	fmt.Printf("[NFSe] recebidas=%d emitidas=%d cancelados=%d semRef=%d outras=%d outrosTipos=%d erros=%d checkpoint=%d ambiente=%s status=%s tipos=%v\n",
		recebidas, emitidas, cancelamentos, semReferencia, outras, outrosTipos, erros, nsu, resumo.TipoAmbiente, resumo.StatusProcessamento, resumo.TiposDocumento)

	return resumo, nil
}

func tipoDocumentoADN(tipo string) string {
	replacer := strings.NewReplacer("-", "", "_", "", " ", "")
	return strings.ToUpper(replacer.Replace(strings.TrimSpace(tipo)))
}

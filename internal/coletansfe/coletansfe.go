// Package coletansfe coleta NFSe (recebida, emitida, cancelamento) via
// Sistema Nacional NFS-e (ADN) — webservice oficial gratuito da SEFAZ.
package coletansfe

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
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

func Run(cfg appconfig.Config) error {
	client, err := adn.NewClient(cfg.CertificadoPfx, cfg.CertificadoSenha)
	if err != nil {
		return fmt.Errorf("criar client ADN: %w", err)
	}

	outDir := filepath.Join(cfg.PastaSaida, "nfse")
	pastaControle := appconfig.PastaControle(cfg.PastaSaida)
	if err := os.MkdirAll(pastaControle, 0o755); err != nil {
		return fmt.Errorf("criar pasta _Controle: %w", err)
	}
	checkpointPath := filepath.Join(pastaControle, ".checkpoint-adn-nsu")

	nsu, err := nfedist.LerCheckpoint(checkpointPath)
	if err != nil {
		return fmt.Errorf("ler checkpoint NFSe: %w", err)
	}
	fmt.Printf("[NFSe] Retomando a partir do NSU %d\n", nsu)

	var recebidas, emitidas, outras, erros, cancelamentos, semReferencia, outrosTipos int

	type registro struct {
		caminho string
		doc     document.Document
	}
	processadas := make(map[string]registro)

	for pagina := 0; pagina < MaxPaginas; pagina++ {
		resp, err := client.BuscarLote(nsu)
		if err != nil {
			log.Printf("[NFSe] buscar lote NSU=%d: %v — parando", nsu, err)
			break
		}

		fmt.Printf("[NFSe] Página %d — NSU inicial %d — status: %s — %d documentos\n",
			pagina+1, nsu, resp.StatusProcessamento, len(resp.LoteDFe))

		if len(resp.LoteDFe) == 0 {
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

			if item.TipoDocumento == "EVENTO" {
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
					if err := catalogo.Registrar(cfg.PastaSaida, reg.doc, reg.caminho); err != nil {
						log.Printf("[NFSe]   NSU %d: aviso ao registrar cancelamento no catálogo: %v", item.NSU, err)
					}
					cancelamentos++
					fmt.Printf("[NFSe]   NSU %d [CANCELAMENTO] renomeado -> %s\n", item.NSU, novoCaminho)
				} else {
					caminho, err := organizer.PlaceEventoSemReferencia(outDir, ev, xmlBytes)
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

			if item.TipoDocumento != "NFSE" {
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

			switch direcao {
			case document.DirecaoRecebida:
				recebidas++
			case document.DirecaoEmitida:
				emitidas++
				doc.Tipo = document.Tipo("NFES-EMITIDA")
			default:
				outras++
				continue
			}

			caminho, err := organizer.PlaceDocument(outDir, doc, ".xml", xmlBytes)
			if err != nil {
				log.Printf("[NFSe]   NSU %d: erro ao gravar: %v", item.NSU, err)
				erros++
				continue
			}
			processadas[doc.Chave] = registro{caminho: caminho, doc: doc}
			if err := catalogo.Registrar(cfg.PastaSaida, doc, caminho); err != nil {
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

	fmt.Println("[NFSe] === Resumo ===")
	fmt.Printf("[NFSe] recebidas=%d emitidas=%d cancelados=%d semRef=%d outras=%d outrosTipos=%d erros=%d checkpoint=%d\n",
		recebidas, emitidas, cancelamentos, semReferencia, outras, outrosTipos, erros, nsu)

	return nil
}

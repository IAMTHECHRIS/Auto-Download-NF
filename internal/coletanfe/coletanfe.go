// Package coletanfe coleta NFe de compra (NFEC) e CTe via NFeDistribuicaoDFe
// — webservice oficial gratuito da SEFAZ, mesmo certificado A1 da NFSe.
//
// IMPORTANTE: essa API pune reinício do zero com bloqueio de 1h ("Consumo
// Indevido"). O checkpoint em disco garante que cada execução continua de
// onde a anterior parou — NUNCA apagar o arquivo de checkpoint sem saber o
// que está fazendo.
package coletanfe

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"io-nf-automation/internal/appconfig"
	"io-nf-automation/internal/catalogo"
	"io-nf-automation/internal/danfe"
	"io-nf-automation/internal/document"
	"io-nf-automation/internal/nfedist"
	"io-nf-automation/internal/organizer"
)

// MaxPaginas é só uma trava de segurança pra nunca rodar pra sempre — o
// loop já para sozinho assim que a SEFAZ diz "nada novo" (cStat=137) ou a
// página vem vazia, então na prática ele varre TUDO que tiver disponível
// numa rodada só, sem precisar de clique manual repetido. Regra oficial da
// SEFAZ: enquanto cStat=138 (achou), pode pedir a próxima na hora; só
// cStat=137 exige esperar. Era 3 antes (cautela de quando esse
// comportamento ainda não tinha sido validado contra a API real,
// 2026-08-23) — 3 é baixo demais pra dar conta de um backlog real na
// primeira coleta.
const MaxPaginas = 50

// geradorDANFE aparece no rodapé do PDF ("Gerado por ...").
const geradorDANFE = "I.O NF Automation"

// MaxRecuperacoesPorRodada limita a recomposição por chave. Busca por chave
// não reinicia NSU, mas ainda é chamada real na SEFAZ; manter lote pequeno
// evita transformar uma recuperação de teste em rajada.
const MaxRecuperacoesPorRodada = 20

type ResumoRecuperacao struct {
	Candidatas  int
	Existentes  int
	Recuperadas int
	Puladas     int
	Erros       []string
	Limitado    bool
}

// esperaEntreUpgrades espaça as consultas avulsas (consChNFe) que
// upgradarParaCompleto dispara pra converter resNFe -> procNFe durante a
// coleta em lote. Mesma cautela do painel (espacamentoMinimoChave): não
// sabemos a cota exata que a SEFAZ tolera pra esse tipo de consulta pontual,
// então evita disparar uma sequência rápida de N notas resumidas na mesma
// rodada.
const esperaEntreUpgrades = 3 * time.Second

// maxUpgradesPorRodada limita as consultas avulsas por chave (consChNFe)
// usadas para transformar resNFe em procNFe/DANFE. A distribuição por lote
// pode trazer dezenas de resumos de uma vez; tentar completar todos na mesma
// execução estoura a cota de 20 consultas/hora e causa cStat=656.
const maxUpgradesPorRodada = 15

// docEmitente pega o CNPJ/CPF do emitente do próprio resNFe; se por algum
// motivo o campo não vier, cai pro CNPJ embutido na chave de acesso (que
// sempre existe, é parte do layout dos 44 dígitos).
func docEmitente(xmlBytes []byte, chave string) string {
	if doc := nfedist.DocDoResNFe(xmlBytes); doc != "" {
		return doc
	}
	return nfedist.CNPJDaChave(chave)
}

// gerarDANFEAoLado tenta montar o DANFE em PDF a partir do MESMO xmlBytes já
// gravado, salvando ao lado do XML (mesmo nome-base, extensão .pdf). Só
// funciona pro schema procNFe_v4.00.xsd (nota completa) — resNFe é resumo,
// não tem campo suficiente (endereço, itens, impostos) pra montar o DANFE
// oficial. Nunca é erro fatal: se falhar, loga e segue — o XML (o dado que
// importa de verdade) já foi salvo antes disso ser chamado.
func gerarDANFEAoLado(caminhoXML string, xmlBytes []byte) {
	pdfBytes, err := danfe.GerarDeXML(xmlBytes, geradorDANFE)
	if err != nil {
		log.Printf("[NFe] aviso: não deu pra gerar o DANFE de %s: %v", filepath.Base(caminhoXML), err)
		return
	}
	caminhoPDF := strings.TrimSuffix(caminhoXML, filepath.Ext(caminhoXML)) + ".pdf"
	if err := os.WriteFile(caminhoPDF, pdfBytes, 0o644); err != nil {
		log.Printf("[NFe] aviso: não deu pra gravar o DANFE de %s: %v", filepath.Base(caminhoXML), err)
	}
}

// upgradarParaCompleto tenta trocar um resNFe (resumo) pelo procNFe (nota
// completa) da MESMA chave, consultando de novo o mesmo webservice em modo
// consChNFe (BuscarPorChave) — é o único jeito de conseguir item/imposto
// suficiente pra gerar o DANFE oficial, já que a distribuição em lote
// (BuscarLote) às vezes só entrega o resumo. Bug real reportado: nota
// baixada como resNFe nunca ganhava PDF, mesmo esperando a próxima coleta —
// o resumo nunca "vira" completo sozinho, é preciso pedir de novo pela
// chave.
//
// Nunca é fatal: se a consulta falhar, vier vazia, ou continuar vindo como
// resumo, devolve ok=false e quem chamou segue com o resumo original (sem
// DANFE, como já era o comportamento). ultimaConsulta é compartilhada entre
// todas as chamadas de uma mesma rodada de Run(), pra respeitar
// esperaEntreUpgrades mesmo com várias notas resumidas na mesma página.
func upgradarParaCompleto(client *nfedist.Client, cfg appconfig.Config, chave string, ultimaConsulta *time.Time) (document.Document, []byte, bool, bool) {
	if !ultimaConsulta.IsZero() {
		if espera := esperaEntreUpgrades - time.Since(*ultimaConsulta); espera > 0 {
			time.Sleep(espera)
		}
	}
	*ultimaConsulta = time.Now()

	res, err := client.BuscarPorChave(cfg.CUFAutor, cfg.CNPJ, chave)
	if err != nil {
		log.Printf("[NFe]   upgrade pra completo (chave %s): consulta falhou: %v — mantendo resumo, sem DANFE", chave, err)
		return document.Document{}, nil, false, false
	}
	if res.CStat != "138" && res.CStat != "137" {
		log.Printf("[NFe]   upgrade pra completo (chave %s): SEFAZ recusou (cStat=%s: %s) — mantendo resumo, sem DANFE", chave, res.CStat, res.XMotivo)
		return document.Document{}, nil, false, res.CStat == "656"
	}
	if len(res.Docs) == 0 {
		log.Printf("[NFe]   upgrade pra completo (chave %s): consulta veio vazia — mantendo resumo, sem DANFE", chave)
		return document.Document{}, nil, false, false
	}

	docZip := res.Docs[0]
	if docZip.Schema != "procNFe_v4.00.xsd" {
		// a SEFAZ pode continuar só devolvendo resumo pra essa chave (ex:
		// não somos o destinatário direto) — não é erro, só não dá pra
		// gerar DANFE dessa nota.
		log.Printf("[NFe]   upgrade pra completo (chave %s): SEFAZ ainda devolveu %s, não procNFe — mantendo resumo, sem DANFE", chave, docZip.Schema)
		return document.Document{}, nil, false, false
	}

	xmlBytes, err := nfedist.DecodeXML(docZip)
	if err != nil {
		log.Printf("[NFe]   upgrade pra completo (chave %s): erro ao decodificar: %v — mantendo resumo, sem DANFE", chave, err)
		return document.Document{}, nil, false, false
	}
	doc, err := document.ParseNFe(xmlBytes)
	if err != nil {
		log.Printf("[NFe]   upgrade pra completo (chave %s): erro ao parsear procNFe: %v — mantendo resumo, sem DANFE", chave, err)
		return document.Document{}, nil, false, false
	}
	return doc, xmlBytes, true, false
}

// BuscarUma pede UMA nota específica pela chave de acesso (44 dígitos) —
// usada quando o usuário apaga um XML sem querer e quer recuperar só
// aquele, sem mexer no checkpoint/NSU da varredura diária. Grava o
// documento e registra no catálogo do mesmo jeito que a coleta normal.
func BuscarUma(cfg appconfig.Config, chave string) (document.Document, string, error) {
	outDir := filepath.Join(cfg.PastaEfetiva(), "NFE", "NFE_COMPRAS")

	client, err := nfedist.NewClient(cfg.CertificadoPfx, cfg.CertificadoSenha, cfg.TpAmb())
	if err != nil {
		return document.Document{}, "", fmt.Errorf("criar client: %w", err)
	}

	res, err := client.BuscarPorChave(cfg.CUFAutor, cfg.CNPJ, chave)
	if err != nil {
		return document.Document{}, "", fmt.Errorf("consultar chave: %w", err)
	}
	if res.CStat != "138" && res.CStat != "137" {
		return document.Document{}, "", fmt.Errorf("SEFAZ recusou (cStat=%s): %s", res.CStat, res.XMotivo)
	}
	if len(res.Docs) == 0 {
		return document.Document{}, "", fmt.Errorf("nenhum documento encontrado pra essa chave")
	}

	docZip := res.Docs[0]
	xmlBytes, err := nfedist.DecodeXML(docZip)
	if err != nil {
		return document.Document{}, "", fmt.Errorf("decodificar XML: %w", err)
	}

	var doc document.Document
	switch docZip.Schema {
	case "resNFe_v1.01.xsd":
		fornecedor, data, valor, chaveResp, cancelada, err := nfedist.ParseResNFe(xmlBytes)
		if err != nil {
			return document.Document{}, "", fmt.Errorf("parsear resNFe: %w", err)
		}
		doc = document.Document{
			Tipo:          document.TipoNFEC,
			Fornecedor:    fornecedor,
			FornecedorDoc: docEmitente(xmlBytes, chaveResp),
			Data:          data,
			Numero:        nfedist.NumeroDaChave(chaveResp),
			Valor:         valor,
			Chave:         chaveResp,
		}
		if cancelada {
			doc.Status = "CANCELADO"
		}
	case "procNFe_v4.00.xsd":
		doc, err = document.ParseNFe(xmlBytes)
		if err != nil {
			return document.Document{}, "", fmt.Errorf("parsear procNFe: %w", err)
		}
	default:
		return document.Document{}, "", fmt.Errorf("essa chave não é de uma NFe (schema retornado: %s) — pode ser um evento de cancelamento", docZip.Schema)
	}

	// Se essa nota já existe no catálogo (ex: chegou antes na coleta em lote
	// como resNFe — resumo, sem itens/impostos suficientes pro DANFE), apaga
	// o arquivo antigo ANTES de gravar o novo. Sem isso, como o nome
	// determinístico (fornecedor+data+tipo+número+valor) é o MESMO, o
	// organizer detecta "colisão" e cria um arquivo duplicado com sufixo
	// "_chave-XXXXXX" em vez de substituir a versão resumida pela completa —
	// bug real reportado (nome de arquivo saindo fora do padrão do projeto).
	if existentes, errCat := catalogo.Listar(cfg.PastaEfetiva()); errCat == nil {
		for _, ex := range existentes {
			if ex.Chave == doc.Chave && ex.Caminho != "" {
				_ = os.Remove(ex.Caminho)
				break
			}
		}
	}

	caminho, err := organizer.PlaceDocumentPlano(outDir, doc, ".xml", xmlBytes)
	if err != nil {
		return document.Document{}, "", fmt.Errorf("gravar arquivo: %w", err)
	}
	if err := catalogo.Registrar(cfg.PastaEfetiva(), doc, caminho); err != nil {
		log.Printf("[NFe] aviso ao registrar no catálogo: %v", err)
	}
	if docZip.Schema == "procNFe_v4.00.xsd" {
		gerarDANFEAoLado(caminho, xmlBytes)
	}

	return doc, caminho, nil
}

// RecuperarArquivosDoCatalogo recompõe XMLs apagados usando as chaves que
// ainda existem no catálogo local. É o caminho seguro para testes em que o
// usuário apagou a pasta de notas: consulta por chave, não por NSU, então não
// volta o checkpoint sequencial nem reinicia a distribuição do zero.
func RecuperarArquivosDoCatalogo(cfg appconfig.Config) ResumoRecuperacao {
	var resumo ResumoRecuperacao

	entradas, err := catalogo.Listar(cfg.PastaEfetiva())
	if err != nil {
		resumo.Erros = append(resumo.Erros, "ler catálogo: "+err.Error())
		return resumo
	}

	var ultimaConsulta time.Time
	for _, entrada := range entradas {
		if entrada.Tipo != string(document.TipoNFEC) {
			continue
		}
		if strings.TrimSpace(entrada.Chave) == "" {
			continue
		}
		resumo.Candidatas++

		if entrada.Caminho != "" {
			if _, err := os.Stat(entrada.Caminho); err == nil {
				resumo.Existentes++
				continue
			}
		}

		if resumo.Recuperadas >= MaxRecuperacoesPorRodada {
			resumo.Limitado = true
			resumo.Puladas++
			continue
		}

		if !ultimaConsulta.IsZero() {
			if espera := esperaEntreUpgrades - time.Since(ultimaConsulta); espera > 0 {
				time.Sleep(espera)
			}
		}
		ultimaConsulta = time.Now()

		if _, _, err := BuscarUma(cfg, entrada.Chave); err != nil {
			resumo.Erros = append(resumo.Erros, fmt.Sprintf("%s: %v", entrada.Chave, err))
			continue
		}
		resumo.Recuperadas++
	}

	return resumo
}

// Resumo é o resultado estruturado de uma rodada de Run() — existe pro
// painel conseguir mostrar um diagnóstico de verdade ("0 notas novas
// porque já está em dia" vs "0 notas novas porque parou no meio do
// caminho, ainda tem mais NSU pra varrer") em vez de um "Atualizado."
// genérico que esconde os dois casos por trás do mesmo texto.
type Resumo struct {
	Paginas       int
	Novas         int
	Cancelamentos int
	SemReferencia int
	OutrosSchemas int
	Erros         int
	NSU           int    // checkpoint depois desta rodada
	UltNSU        string // último "ultNSU" que a SEFAZ informou
	MaxNSU        string // último "maxNSU" que a SEFAZ informou — se UltNSU != MaxNSU, ainda tem mais

	// Preenchidos quando a ÚLTIMA página veio rejeitada (cStat != 137/138)
	// — antes isso só ia pro log.Printf, que em build windowsgui não vai
	// pra lugar nenhum visível. Agora fica no retorno pro painel conseguir
	// mostrar o motivo de verdade.
	CStat   string
	XMotivo string

	// AutoCorrigido: quando uma rejeição revela o NSU certo pra continuar
	// (comportamento documentado desse webservice — reiniciar do NSU
	// errado é rejeitado, mas a própria resposta de rejeição informa o
	// ultNSU correto), o programa semeia o checkpoint sozinho com esse
	// valor e tenta de novo na mesma rodada, uma vez só, em vez de só
	// desistir. Fica registrado aqui pra aparecer no diagnóstico.
	AutoCorrigido    bool
	NSUAutoCorrigido int
}

// EmDia diz se a rodada varreu até o fim (NSU alcançou o máximo que a SEFAZ
// tinha disponível) — quando falso, "0 notas novas" pode só significar que
// as MaxPaginas desta rodada acabaram antes de chegar em algo novo, não que
// não existe nada novo.
func (r Resumo) EmDia() bool {
	return r.UltNSU != "" && r.UltNSU == r.MaxNSU
}

func Run(cfg appconfig.Config) (Resumo, error) {
	var resumo Resumo
	outDir := filepath.Join(cfg.PastaEfetiva(), "NFE", "NFE_COMPRAS")
	pastaControle := appconfig.PastaControle(cfg.PastaEfetiva())
	if err := os.MkdirAll(pastaControle, 0o755); err != nil {
		return resumo, fmt.Errorf("criar pasta _Controle: %w", err)
	}
	checkpointPath := filepath.Join(pastaControle, ".checkpoint-nfedist-nsu")

	client, err := nfedist.NewClient(cfg.CertificadoPfx, cfg.CertificadoSenha, cfg.TpAmb())
	if err != nil {
		return resumo, fmt.Errorf("criar client: %w", err)
	}

	nsu, err := nfedist.LerCheckpoint(checkpointPath)
	if err != nil {
		return resumo, fmt.Errorf("ler checkpoint: %w", err)
	}
	fmt.Printf("[NFe] Retomando a partir do NSU %d\n", nsu)

	var resumos, eventos, outrosSchemas, erros, cancelamentos, semReferencia int
	var ultimaConsultaUpgrade time.Time
	var upgradesTentados int
	var upgradesBloqueados bool

	type registro struct {
		caminho string
		doc     document.Document
	}
	processadas := make(map[string]registro)

	// Pré-carrega o que o catálogo já tem de rodadas anteriores — sem isso
	// dois problemas: 1) reprocessar o mesmo NSU (ex: rodada que foi
	// interrompida e recomeçou) grava a MESMA nota de novo, colidindo com o
	// arquivo já existente e criando uma cópia com sufixo "_chave-XXXXXX"
	// (bug real reportado — arquivo duplicado); 2) um evento de
	// cancelamento pra uma nota de uma rodada ANTERIOR não achava a nota
	// original (só olhava as processadas NESTA rodada) e caía no caminho de
	// "sem referência", perdendo a marcação de CANCELADO na nota certa.
	if existentes, err := catalogo.Listar(cfg.PastaEfetiva()); err != nil {
		log.Printf("[NFe] aviso: não consegui pré-carregar o catálogo (seguindo sem isso): %v", err)
	} else {
		for _, ex := range existentes {
			if ex.Chave == "" || ex.Caminho == "" {
				continue
			}
			if _, statErr := os.Stat(ex.Caminho); statErr != nil {
				continue // arquivo não existe mais no disco, não conta como "já processado"
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

	for pagina := 0; pagina < MaxPaginas; pagina++ {
		res, err := client.BuscarLote(cfg.CUFAutor, cfg.CNPJ, nsu)
		if err != nil {
			log.Printf("[NFe] buscar lote NSU=%d: %v — parando", nsu, err)
			break
		}
		resumo.Paginas++
		resumo.UltNSU = res.UltNSU
		resumo.MaxNSU = res.MaxNSU
		resumo.CStat = res.CStat
		resumo.XMotivo = res.XMotivo

		fmt.Printf("[NFe] Página %d — cStat=%s (%s) — %d documentos — ultNSU=%s maxNSU=%s\n",
			pagina+1, res.CStat, res.XMotivo, len(res.Docs), res.UltNSU, res.MaxNSU)

		if res.CStat != "138" && res.CStat != "137" {
			// auto-recuperação: se a rejeição revelou um NSU maior que o
			// nosso (comportamento documentado — reinício errado devolve o
			// NSU certo no próprio ultNSU), semeia o checkpoint com isso e
			// tenta UMA vez de novo antes de desistir. Só uma tentativa —
			// não vira loop se a SEFAZ continuar rejeitando por outro motivo.
			if !resumo.AutoCorrigido {
				if nsuRevelado, convErr := strconv.Atoi(res.UltNSU); convErr == nil && nsuRevelado > nsu {
					log.Printf("[NFe] rejeitado (cStat=%s: %s) — SEFAZ revelou NSU %d; checkpoint semeado. Parando agora para respeitar a janela da SEFAZ.", res.CStat, res.XMotivo, nsuRevelado)
					nsu = nsuRevelado
					if err := nfedist.SalvarCheckpoint(checkpointPath, nsu); err != nil {
						log.Printf("[NFe] erro ao salvar checkpoint corrigido: %v", err)
					}
					resumo.AutoCorrigido = true
					resumo.NSUAutoCorrigido = nsu
					break
				}
			}
			log.Printf("[NFe] cStat inesperado, parando por segurança: %s — %s", res.CStat, res.XMotivo)
			break
		}

		for _, docZip := range res.Docs {
			xmlBytes, err := nfedist.DecodeXML(docZip)
			if err != nil {
				log.Printf("[NFe]   NSU %s: erro ao decodificar: %v", docZip.NSU, err)
				erros++
				continue
			}

			switch docZip.Schema {
			case "resNFe_v1.01.xsd":
				fornecedor, data, valor, chave, cancelada, err := nfedist.ParseResNFe(xmlBytes)
				if err != nil {
					log.Printf("[NFe]   NSU %s: erro ao parsear resNFe: %v", docZip.NSU, err)
					erros++
					continue
				}
				doc := document.Document{
					Tipo:          document.TipoNFEC,
					Fornecedor:    fornecedor,
					FornecedorDoc: docEmitente(xmlBytes, chave),
					Data:          data,
					Numero:        nfedist.NumeroDaChave(chave),
					Valor:         valor,
					Chave:         chave,
				}
				if cancelada {
					doc.Status = "CANCELADO"
				}
				if reg, ja := processadas[doc.Chave]; ja && reg.doc.Status == doc.Status {
					// mesma nota, mesmo status, já gravada antes (rodada
					// anterior ou reprocessamento do mesmo NSU) — não duplica.
					fmt.Printf("[NFe]   NSU %s [resNFe] já existe (%s) -> pulando\n", docZip.NSU, reg.caminho)
					continue
				}
				// resNFe é resumo — não tem item/imposto suficiente pro
				// DANFE oficial. Antes disso ficava definitivo (a nota
				// nunca ganhava PDF). Agora tenta upgradar pra procNFe
				// (nota completa) na hora, pela mesma chave — se conseguir,
				// grava a versão completa (e o DANFE junto) em vez do
				// resumo. Notas já canceladas não precisam de DANFE, então
				// pulam a consulta extra.
				xmlParaGravar := xmlBytes
				schemaGravado := docZip.Schema
				if !cancelada && !upgradesBloqueados && upgradesTentados < maxUpgradesPorRodada {
					upgradesTentados++
					if completo, xmlCompleto, ok, bloqueado := upgradarParaCompleto(client, cfg, chave, &ultimaConsultaUpgrade); ok {
						doc = completo
						xmlParaGravar = xmlCompleto
						schemaGravado = "procNFe_v4.00.xsd"
					} else if bloqueado {
						upgradesBloqueados = true
						log.Printf("[NFe]   limite/bloqueio SEFAZ detectado no upgrade por chave — upgrades pausados até a próxima execução; a coleta por lote continua salvando resumos")
					}
				} else if !cancelada && upgradesTentados >= maxUpgradesPorRodada && !upgradesBloqueados {
					upgradesBloqueados = true
					log.Printf("[NFe]   limite local de %d upgrades por rodada atingido — próximos resumos serão salvos sem DANFE e podem ser completados em outra execução", maxUpgradesPorRodada)
				}

				caminho, err := organizer.PlaceDocumentPlano(outDir, doc, ".xml", xmlParaGravar)
				if err != nil {
					log.Printf("[NFe]   NSU %s: erro ao gravar: %v", docZip.NSU, err)
					erros++
					continue
				}
				processadas[doc.Chave] = registro{caminho: caminho, doc: doc}
				if err := catalogo.Registrar(cfg.PastaEfetiva(), doc, caminho); err != nil {
					log.Printf("[NFe]   NSU %s: aviso ao registrar no catálogo: %v", docZip.NSU, err)
				}
				if schemaGravado == "procNFe_v4.00.xsd" {
					gerarDANFEAoLado(caminho, xmlParaGravar)
					resumos++
					fmt.Printf("[NFe]   NSU %s [resNFe -> upgrade completo] -> %s\n", docZip.NSU, caminho)
				} else {
					resumos++
					fmt.Printf("[NFe]   NSU %s [resNFe] -> %s\n", docZip.NSU, caminho)
				}

			case "procNFe_v4.00.xsd":
				doc, err := document.ParseNFe(xmlBytes)
				if err != nil {
					log.Printf("[NFe]   NSU %s: erro ao parsear procNFe: %v", docZip.NSU, err)
					erros++
					continue
				}
				if reg, ja := processadas[doc.Chave]; ja && reg.doc.Status == doc.Status {
					fmt.Printf("[NFe]   NSU %s [procNFe] já existe (%s) -> pulando\n", docZip.NSU, reg.caminho)
					continue
				}
				caminho, err := organizer.PlaceDocumentPlano(outDir, doc, ".xml", xmlBytes)
				if err != nil {
					log.Printf("[NFe]   NSU %s: erro ao gravar: %v", docZip.NSU, err)
					erros++
					continue
				}
				processadas[doc.Chave] = registro{caminho: caminho, doc: doc}
				if err := catalogo.Registrar(cfg.PastaEfetiva(), doc, caminho); err != nil {
					log.Printf("[NFe]   NSU %s: aviso ao registrar no catálogo: %v", docZip.NSU, err)
				}
				gerarDANFEAoLado(caminho, xmlBytes)
				resumos++
				fmt.Printf("[NFe]   NSU %s [procNFe completo] -> %s\n", docZip.NSU, caminho)

			case "procEventoNFe_v1.00.xsd":
				ev, err := nfedist.ParseEventoNFe(xmlBytes)
				if err != nil {
					log.Printf("[NFe]   NSU %s: erro ao parsear evento: %v", docZip.NSU, err)
					erros++
					continue
				}
				if !ev.Cancelamento {
					eventos++
					continue
				}
				reg, achou := processadas[ev.ChaveOriginal]
				if achou && reg.doc.Status == "CANCELADO" {
					continue
				}
				if achou {
					novoCaminho, err := organizer.MarcarCancelado(reg.caminho, reg.doc)
					if err != nil {
						log.Printf("[NFe]   NSU %s: erro ao marcar cancelado: %v", docZip.NSU, err)
						erros++
						continue
					}
					reg.caminho = novoCaminho
					reg.doc.Status = "CANCELADO"
					processadas[ev.ChaveOriginal] = reg
					if err := catalogo.Registrar(cfg.PastaEfetiva(), reg.doc, reg.caminho); err != nil {
						log.Printf("[NFe]   NSU %s: aviso ao registrar cancelamento no catálogo: %v", docZip.NSU, err)
					}
					cancelamentos++
					fmt.Printf("[NFe]   NSU %s [CANCELAMENTO] renomeado -> %s\n", docZip.NSU, novoCaminho)
				} else {
					semReferencia++
				}

			default:
				outrosSchemas++
			}
		}

		novoNSU, err := strconv.Atoi(res.UltNSU)
		if err != nil {
			log.Printf("[NFe] ultNSU inválido %q: %v — parando", res.UltNSU, err)
			break
		}
		nsu = novoNSU

		if err := nfedist.SalvarCheckpoint(checkpointPath, nsu); err != nil {
			log.Printf("[NFe] erro ao salvar checkpoint: %v", err)
		}

		if len(res.Docs) == 0 {
			break
		}
	}

	resumo.Novas = resumos
	resumo.Cancelamentos = cancelamentos
	resumo.SemReferencia = semReferencia
	resumo.OutrosSchemas = outrosSchemas
	resumo.Erros = erros
	resumo.NSU = nsu

	fmt.Println("[NFe] === Resumo ===")
	fmt.Printf("[NFe] processados=%d cancelados=%d semRef=%d outrosSchemas=%d erros=%d checkpoint=%d\n",
		resumos, cancelamentos, semReferencia, outrosSchemas, erros, nsu)

	return resumo, nil
}

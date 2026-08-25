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

	"io-nf-automation/internal/appconfig"
	"io-nf-automation/internal/catalogo"
	"io-nf-automation/internal/document"
	"io-nf-automation/internal/nfedist"
	"io-nf-automation/internal/organizer"
)

// MaxPaginas — cautela deliberada: não varrer tudo numa tacada só.
const MaxPaginas = 3

// BuscarUma pede UMA nota específica pela chave de acesso (44 dígitos) —
// usada quando o usuário apaga um XML sem querer e quer recuperar só
// aquele, sem mexer no checkpoint/NSU da varredura diária. Grava o
// documento e registra no catálogo do mesmo jeito que a coleta normal.
func BuscarUma(cfg appconfig.Config, chave string) (document.Document, string, error) {
	outDir := filepath.Join(cfg.PastaSaida, "nfe-compras")

	client, err := nfedist.NewClient(cfg.CertificadoPfx, cfg.CertificadoSenha)
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
			Tipo:       document.TipoNFEC,
			Fornecedor: fornecedor,
			Data:       data,
			Numero:     nfedist.NumeroDaChave(chaveResp),
			Valor:      valor,
			Chave:      chaveResp,
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

	caminho, err := organizer.PlaceDocument(outDir, doc, ".xml", xmlBytes)
	if err != nil {
		return document.Document{}, "", fmt.Errorf("gravar arquivo: %w", err)
	}
	if err := catalogo.Registrar(cfg.PastaSaida, doc, caminho); err != nil {
		log.Printf("[NFe] aviso ao registrar no catálogo: %v", err)
	}

	return doc, caminho, nil
}

func Run(cfg appconfig.Config) error {
	outDir := filepath.Join(cfg.PastaSaida, "nfe-compras")
	pastaControle := appconfig.PastaControle(cfg.PastaSaida)
	if err := os.MkdirAll(pastaControle, 0o755); err != nil {
		return fmt.Errorf("criar pasta _Controle: %w", err)
	}
	checkpointPath := filepath.Join(pastaControle, ".checkpoint-nfedist-nsu")

	client, err := nfedist.NewClient(cfg.CertificadoPfx, cfg.CertificadoSenha)
	if err != nil {
		return fmt.Errorf("criar client: %w", err)
	}

	nsu, err := nfedist.LerCheckpoint(checkpointPath)
	if err != nil {
		return fmt.Errorf("ler checkpoint: %w", err)
	}
	fmt.Printf("[NFe] Retomando a partir do NSU %d\n", nsu)

	var resumos, eventos, outrosSchemas, erros, cancelamentos, semReferencia int

	type registro struct {
		caminho string
		doc     document.Document
	}
	processadas := make(map[string]registro)

	for pagina := 0; pagina < MaxPaginas; pagina++ {
		res, err := client.BuscarLote(cfg.CUFAutor, cfg.CNPJ, nsu)
		if err != nil {
			log.Printf("[NFe] buscar lote NSU=%d: %v — parando", nsu, err)
			break
		}

		fmt.Printf("[NFe] Página %d — cStat=%s (%s) — %d documentos — ultNSU=%s maxNSU=%s\n",
			pagina+1, res.CStat, res.XMotivo, len(res.Docs), res.UltNSU, res.MaxNSU)

		if res.CStat != "138" && res.CStat != "137" {
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
					Tipo:       document.TipoNFEC,
					Fornecedor: fornecedor,
					Data:       data,
					Numero:     nfedist.NumeroDaChave(chave),
					Valor:      valor,
					Chave:      chave,
				}
				if cancelada {
					doc.Status = "CANCELADO"
				}
				caminho, err := organizer.PlaceDocument(outDir, doc, ".xml", xmlBytes)
				if err != nil {
					log.Printf("[NFe]   NSU %s: erro ao gravar: %v", docZip.NSU, err)
					erros++
					continue
				}
				processadas[doc.Chave] = registro{caminho: caminho, doc: doc}
				if err := catalogo.Registrar(cfg.PastaSaida, doc, caminho); err != nil {
					log.Printf("[NFe]   NSU %s: aviso ao registrar no catálogo: %v", docZip.NSU, err)
				}
				resumos++
				fmt.Printf("[NFe]   NSU %s [resNFe] -> %s\n", docZip.NSU, caminho)

			case "procNFe_v4.00.xsd":
				doc, err := document.ParseNFe(xmlBytes)
				if err != nil {
					log.Printf("[NFe]   NSU %s: erro ao parsear procNFe: %v", docZip.NSU, err)
					erros++
					continue
				}
				caminho, err := organizer.PlaceDocument(outDir, doc, ".xml", xmlBytes)
				if err != nil {
					log.Printf("[NFe]   NSU %s: erro ao gravar: %v", docZip.NSU, err)
					erros++
					continue
				}
				processadas[doc.Chave] = registro{caminho: caminho, doc: doc}
				if err := catalogo.Registrar(cfg.PastaSaida, doc, caminho); err != nil {
					log.Printf("[NFe]   NSU %s: aviso ao registrar no catálogo: %v", docZip.NSU, err)
				}
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
					if err := catalogo.Registrar(cfg.PastaSaida, reg.doc, reg.caminho); err != nil {
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

	fmt.Println("[NFe] === Resumo ===")
	fmt.Printf("[NFe] processados=%d cancelados=%d semRef=%d outrosSchemas=%d erros=%d checkpoint=%d\n",
		resumos, cancelamentos, semReferencia, outrosSchemas, erros, nsu)

	return nil
}

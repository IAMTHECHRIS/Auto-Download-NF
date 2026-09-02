// Package document extrai os campos que importam (fornecedor, data, número,
// valor) dos XMLs oficiais de NFe/NFCe/NFSe. Ler o XML é muito mais confiável
// que tentar extrair texto de PDF: é o mesmo dado que a SEFAZ validou.
package document

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// Tipo é a categoria do documento, usada tanto pro parser quanto pro nome da
// pasta/arquivo final (NFEC, NFES, CUPOM, FAT).
type Tipo string

const (
	TipoNFEC        Tipo = "NFEC"         // NFe de compra (entrada)
	TipoNFES        Tipo = "NFES"         // NFSe de serviço recebida (entrada)
	TipoNFESEmitida Tipo = "NFES-EMITIDA" // NFSe de serviço emitida (saída)
	TipoCUPOM       Tipo = "CUPOM"        // NFCe (cupom fiscal eletrônico)
	TipoFAT         Tipo = "FAT"          // Fatura (sem XML, vem de e-mail)

	// Reservados — pasta/convenção já prevista (ver internal/verificacao),
	// mas sem coleta automática pra esses tipos ainda.
	TipoNFER Tipo = "NFER" // NFe de remessa, entrada
	TipoNFSR Tipo = "NFSR" // NFe de remessa, saída
)

// Document é o resultado padronizado depois de extrair um XML — é o que o
// resto do pipeline (renomeio, organização de pasta) consome, independente
// de ter vindo de NFe, NFCe ou NFSe.
type Document struct {
	Tipo          Tipo
	Fornecedor    string
	FornecedorDoc string // CNPJ (ou CPF) do fornecedor, só dígitos — formatação fica na exibição
	Data          time.Time
	Numero        string
	Valor         float64
	OP            string // vazio por enquanto — fase 2 do projeto (vínculo ERP)
	Status        string // vazio = normal; "CANCELADO" quando um evento referencia essa nota
	Chave         string // chave de acesso — usada pra casar evento de cancelamento com a nota
}

// --- NFe / NFCe (mesmo schema oficial; mod=55 é NFe, mod=65 é NFCe) ---

type nfeProc struct {
	XMLName xml.Name `xml:"nfeProc"`
	NFe     struct {
		InfNFe struct {
			Id  string `xml:"Id,attr"` // ex: "NFe35180812345678000199550010000000011234567890" — chave é sem o prefixo "NFe"
			Ide struct {
				Mod   string `xml:"mod"`   // 55=NFe, 65=NFCe
				NNF   string `xml:"nNF"`   // número da nota
				DhEmi string `xml:"dhEmi"` // data/hora de emissão, RFC3339
			} `xml:"ide"`
			Emit struct {
				CNPJ  string `xml:"CNPJ"`  // CNPJ do fornecedor
				CPF   string `xml:"CPF"`   // emitente pessoa física usa CPF em vez de CNPJ
				XNome string `xml:"xNome"` // razão social do fornecedor
			} `xml:"emit"`
			Total struct {
				ICMSTot struct {
					VNF string `xml:"vNF"` // valor total da nota
				} `xml:"ICMSTot"`
			} `xml:"total"`
		} `xml:"infNFe"`
	} `xml:"NFe"`
}

// ParseNFe interpreta o XML oficial de NFe/NFCe (schema público da SEFAZ,
// tag <nfeProc>). Distingue NFe de NFCe pelo campo <mod> (55 vs 65).
func ParseNFe(raw []byte) (Document, error) {
	var p nfeProc
	if err := xml.Unmarshal(raw, &p); err != nil {
		return Document{}, fmt.Errorf("parse NFe/NFCe: %w", err)
	}

	inf := p.NFe.InfNFe
	tipo := TipoNFEC
	if inf.Ide.Mod == "65" {
		tipo = TipoCUPOM
	}

	dt, err := time.Parse(time.RFC3339, inf.Ide.DhEmi)
	if err != nil {
		// alguns emissores mandam sem timezone explícito
		dt, err = time.Parse("2006-01-02T15:04:05", inf.Ide.DhEmi)
		if err != nil {
			return Document{}, fmt.Errorf("parse data emissão %q: %w", inf.Ide.DhEmi, err)
		}
	}

	var valor float64
	if _, err := fmt.Sscanf(inf.Total.ICMSTot.VNF, "%f", &valor); err != nil {
		return Document{}, fmt.Errorf("parse valor %q: %w", inf.Total.ICMSTot.VNF, err)
	}

	chave := strings.TrimPrefix(inf.Id, "NFe")

	doc := inf.Emit.CNPJ
	if doc == "" {
		doc = inf.Emit.CPF // emitente pessoa física
	}

	return Document{
		Tipo:          tipo,
		Fornecedor:    inf.Emit.XNome,
		FornecedorDoc: doc,
		Data:          dt,
		Numero:        inf.Ide.NNF,
		Valor:         valor,
		Chave:         chave,
	}, nil
}

// --- NFSe (Sistema Nacional) ---
//
// Schema confirmado em 2026-08-23 contra XML real da API ADN
// (https://adn.nfse.gov.br/contribuintes/DFe/{NSU}, campo ArquivoXml =
// base64(gzip(xml))). Estrutura: <NFSe><infNFSe>{nNFSe,dhProc,emit,toma,
// valores,DPS{infDPS{dCompet}}}</infNFSe></NFSe>.
type nfseNacional struct {
	XMLName xml.Name `xml:"NFSe"`
	InfNFSe struct {
		NNFSe  string `xml:"nNFSe"`  // número da NFSe
		DhProc string `xml:"dhProc"` // data/hora de processamento (fallback)
		Emit   struct {
			CNPJ  string `xml:"CNPJ"`
			CPF   string `xml:"CPF"`
			XNome string `xml:"xNome"` // prestador do serviço
		} `xml:"emit"`
		Valores struct {
			VLiq string `xml:"vLiq"` // valor líquido
		} `xml:"valores"`
		DPS struct {
			InfDPS struct {
				DCompet string `xml:"dCompet"` // data de competência
				// <toma> fica ANINHADO aqui dentro de infDPS, não é irmão
				// direto de <emit> lá em cima — confirmado contra XML real
				// em 2026-08-23 (erro na primeira versão deste parser).
				Toma struct {
					CNPJ  string `xml:"CNPJ"`
					CPF   string `xml:"CPF"`
					XNome string `xml:"xNome"` // tomador do serviço
				} `xml:"toma"`
			} `xml:"infDPS"`
		} `xml:"DPS"`
	} `xml:"infNFSe"`
}

// Direcao indica se a empresa é quem emitiu (prestou o serviço) ou quem
// recebeu (tomou o serviço) — determina se o custo entra no fluxo de
// despesas (NFES) ou é receita (emitida).
type Direcao string

const (
	DirecaoRecebida Direcao = "recebida" // empresa é a tomadora — é custo
	DirecaoEmitida  Direcao = "emitida"  // empresa é a prestadora — é receita
	DirecaoOutro    Direcao = "outro"    // nem emit nem toma bate com o CNPJ da empresa
)

// ParseNFSeNacional interpreta o XML da NFSe do Sistema Nacional Já
// descomprimido (ver io-nf-automation/internal/adn pro passo de
// base64+gzip). meuCNPJ é usado só pra apontar Direcao — o Document sempre
// carrega o FORNECEDOR (prestador), independente da direção; quem consome
// decide o que fazer com uma nota emitida (hoje o padrão de pasta só cobre
// custos recebidos).
func ParseNFSeNacional(raw []byte, meuCNPJ string) (Document, Direcao, error) {
	var p nfseNacional
	if err := xml.Unmarshal(raw, &p); err != nil {
		return Document{}, "", fmt.Errorf("parse NFSe nacional: %w", err)
	}

	inf := p.InfNFSe
	dataStr := inf.DPS.InfDPS.DCompet
	if dataStr == "" {
		dataStr = inf.DhProc
	}
	dt, err := time.Parse("2006-01-02", dataStr)
	if err != nil {
		dt, err = time.Parse(time.RFC3339, dataStr)
		if err != nil {
			return Document{}, "", fmt.Errorf("parse data NFSe %q: %w", dataStr, err)
		}
	}

	var valor float64
	if _, err := fmt.Sscanf(inf.Valores.VLiq, "%f", &valor); err != nil {
		return Document{}, "", fmt.Errorf("parse valor NFSe %q: %w", inf.Valores.VLiq, err)
	}

	meuDoc := somenteDigitos(meuCNPJ)
	emitDoc := primeiroNaoVazio(somenteDigitos(inf.Emit.CNPJ), somenteDigitos(inf.Emit.CPF))
	tomaDoc := primeiroNaoVazio(somenteDigitos(inf.DPS.InfDPS.Toma.CNPJ), somenteDigitos(inf.DPS.InfDPS.Toma.CPF))

	direcao := DirecaoOutro
	switch meuDoc {
	case tomaDoc:
		direcao = DirecaoRecebida
	case emitDoc:
		direcao = DirecaoEmitida
	}

	return Document{
		Tipo:          TipoNFES,
		Fornecedor:    inf.Emit.XNome, // prestador — quem emitiu a nota
		FornecedorDoc: emitDoc,
		Data:          dt,
		Numero:        inf.NNFSe,
		Valor:         valor,
	}, direcao, nil
}

func somenteDigitos(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func primeiroNaoVazio(valores ...string) string {
	for _, v := range valores {
		if v != "" {
			return v
		}
	}
	return ""
}

// --- Evento (cancelamento/correção de NFSe) ---
//
// Schema confirmado em 2026-08-23: tag raiz <evento>, motivo em
// pedRegEvento/infPedReg/e101101 (101101 = código de cancelamento).
type evento struct {
	XMLName   xml.Name `xml:"evento"`
	InfEvento struct {
		DhProc       string `xml:"dhProc"`
		PedRegEvento struct {
			InfPedReg struct {
				DhEvento string `xml:"dhEvento"`
				ChNFSe   string `xml:"chNFSe"` // chave da nota original cancelada
				E101101  struct {
					XDesc   string `xml:"xDesc"`   // ex: "Cancelamento de NFS-e"
					XMotivo string `xml:"xMotivo"` // motivo em texto livre
				} `xml:"e101101"`
			} `xml:"infPedReg"`
		} `xml:"pedRegEvento"`
	} `xml:"infEvento"`
}

// EventoCancelamento é o resultado padronizado de um evento de cancelamento
// — não é um "documento" com fornecedor/valor, é um registro histórico que
// referencia a nota original pela chave de acesso.
type EventoCancelamento struct {
	Data          time.Time
	ChaveOriginal string
	Descricao     string
	Motivo        string
}

// ParseEvento interpreta um XML de evento (cancelamento/correção).
func ParseEvento(raw []byte) (EventoCancelamento, error) {
	var p evento
	if err := xml.Unmarshal(raw, &p); err != nil {
		return EventoCancelamento{}, fmt.Errorf("parse evento: %w", err)
	}

	inf := p.InfEvento
	dataStr := inf.PedRegEvento.InfPedReg.DhEvento
	if dataStr == "" {
		dataStr = inf.DhProc
	}
	dt, err := time.Parse(time.RFC3339, dataStr)
	if err != nil {
		return EventoCancelamento{}, fmt.Errorf("parse data evento %q: %w", dataStr, err)
	}

	return EventoCancelamento{
		Data:          dt,
		ChaveOriginal: inf.PedRegEvento.InfPedReg.ChNFSe,
		Descricao:     inf.PedRegEvento.InfPedReg.E101101.XDesc,
		Motivo:        inf.PedRegEvento.InfPedReg.E101101.XMotivo,
	}, nil
}

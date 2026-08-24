package nfedist

import (
	"encoding/xml"
	"fmt"
	"time"
)

// ResumoNFe é o resultado de um <resNFe> — schema resNFe_v1.01.xsd, campos
// definidos na Nota Técnica 2014/002.
//
// ATENÇÃO: escrito com base na especificação oficial, NÃO validado ainda
// contra um XML real (bati o bloqueio de 1h antes de conseguir salvar um
// exemplo). Mesma lição da NFSe: não confiar cegamente até testar contra
// dado real — próximo passo depois do bloqueio passar é decodificar um
// resNFe de verdade e conferir se os nomes de tag batem exatamente com
// isso aqui.
type resNFe struct {
	XMLName xml.Name `xml:"resNFe"`
	ChNFe   string   `xml:"chNFe"`   // chave de acesso (44 dígitos)
	CNPJ    string   `xml:"CNPJ"`    // CNPJ do emitente
	CPF     string   `xml:"CPF"`     // ou CPF, se emitente for pessoa física
	XNome   string   `xml:"xNome"`   // razão social do emitente
	DhEmi   string   `xml:"dhEmi"`   // data/hora de emissão
	TpNF    string   `xml:"tpNF"`    // 0=entrada, 1=saída (do ponto de vista do emitente)
	VNF     string   `xml:"vNF"`     // valor total da nota
	CSitNFe string   `xml:"cSitNFe"` // situação: 1=autorizada, 2=cancelada, 3=denegada
}

// ParseResNFe interpreta o resumo (não é a nota completa, só os campos
// principais — mas devem ser suficientes pro nosso padrão de nome de
// arquivo: fornecedor, data, valor, chave).
func ParseResNFe(raw []byte) (fornecedor string, data time.Time, valor float64, chave string, cancelada bool, err error) {
	var r resNFe
	if err = xml.Unmarshal(raw, &r); err != nil {
		return "", time.Time{}, 0, "", false, fmt.Errorf("parse resNFe: %w", err)
	}

	data, err = time.Parse(time.RFC3339, r.DhEmi)
	if err != nil {
		data, err = time.Parse("2006-01-02T15:04:05", r.DhEmi)
		if err != nil {
			return "", time.Time{}, 0, "", false, fmt.Errorf("parse data resNFe %q: %w", r.DhEmi, err)
		}
	}

	if _, err = fmt.Sscanf(r.VNF, "%f", &valor); err != nil {
		return "", time.Time{}, 0, "", false, fmt.Errorf("parse valor resNFe %q: %w", r.VNF, err)
	}

	// cSitNFe: 2 = cancelada (ver nota de incerteza acima)
	cancelada = r.CSitNFe == "2"

	return r.XNome, data, valor, r.ChNFe, cancelada, nil
}

// NumeroDaChave extrai o número da nota a partir da chave de acesso (44
// dígitos) — o resNFe não traz o número separado. Layout oficial da chave:
// UF(2) AAMM(4) CNPJ(14) mod(2) serie(3) nNF(9) tpEmis(1) cNF(8) cDV(1).
func NumeroDaChave(chave string) string {
	if len(chave) != 44 {
		return chave // formato inesperado — devolve a chave inteira como fallback
	}
	nNF := chave[25:34]
	// remove zeros à esquerda pra ficar no mesmo estilo das notas de NFSe
	i := 0
	for i < len(nNF)-1 && nNF[i] == '0' {
		i++
	}
	return nNF[i:]
}

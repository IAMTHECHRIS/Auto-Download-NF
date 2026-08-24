package nfedist

import (
	"encoding/xml"
	"fmt"
	"time"
)

// procEventoNFe — evento completo e assinado (cancelamento, carta de
// correção, etc). Schema oficial NFe: <procEventoNFe><evento><infEvento>.
//
// ATENÇÃO: escrito com base na spec oficial (mesmo padrão do evento de
// cancelamento de NFe, bem documentado e estável há anos), mas AINDA NÃO
// validado contra um XML real — só contei quantos apareceram (3, no teste
// de 2026-08-23), não salvei o conteúdo pra conferir os nomes de tag.
// Validar no próximo run do timer diário e corrigir aqui se algo não bater
// — mesma disciplina usada pro resNFe e pro <toma> da NFSe antes.
type procEventoNFe struct {
	XMLName xml.Name `xml:"procEventoNFe"`
	Evento  struct {
		InfEvento struct {
			COrgao   string `xml:"cOrgao"`
			TpAmb    string `xml:"tpAmb"`
			CNPJ     string `xml:"CNPJ"`
			ChNFe    string `xml:"chNFe"`    // chave da nota/CTe original
			DhEvento string `xml:"dhEvento"` // data/hora do evento
			TpEvento string `xml:"tpEvento"` // 110111=cancelamento, 110110=carta de correção, etc
			XEvento  string `xml:"detEvento>xEvento,omitempty"`
		} `xml:"infEvento"`
	} `xml:"evento"`
}

// EventoNFe é o resultado padronizado.
type EventoNFe struct {
	Data          time.Time
	ChaveOriginal string
	TipoEvento    string // código: 110111=cancelamento
	Cancelamento  bool
}

// TpEventoCancelamento é o código oficial de cancelamento de NFe/CTe.
const TpEventoCancelamento = "110111"

func ParseEventoNFe(raw []byte) (EventoNFe, error) {
	var p procEventoNFe
	if err := xml.Unmarshal(raw, &p); err != nil {
		return EventoNFe{}, fmt.Errorf("parse procEventoNFe: %w", err)
	}

	inf := p.Evento.InfEvento
	dt, err := time.Parse(time.RFC3339, inf.DhEvento)
	if err != nil {
		dt, err = time.Parse("2006-01-02T15:04:05", inf.DhEvento)
		if err != nil {
			return EventoNFe{}, fmt.Errorf("parse data evento %q: %w", inf.DhEvento, err)
		}
	}

	return EventoNFe{
		Data:          dt,
		ChaveOriginal: inf.ChNFe,
		TipoEvento:    inf.TpEvento,
		Cancelamento:  inf.TpEvento == TpEventoCancelamento,
	}, nil
}

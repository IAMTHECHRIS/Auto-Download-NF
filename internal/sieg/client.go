// Package sieg fala com a API de download em lote do SIEG
// (https://api.sieg.com/BaixarXmls). Documentação:
// https://sieg.movidesk.com/kb/pt-br/article/356445
package sieg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const baixarXmlsURL = "https://api.sieg.com/BaixarXmls"

// XmlType — tipo de documento pedido à API do SIEG.
type XmlType int

const (
	XmlTypeNFe  XmlType = 1
	XmlTypeCTe  XmlType = 2
	XmlTypeNFSe XmlType = 3
	XmlTypeNFCe XmlType = 4
	XmlTypeCFe  XmlType = 5
)

// Client fala com a API do SIEG. ApiKey vem do painel: Minha Conta →
// Integrações API SIEG.
type Client struct {
	ApiKey     string
	HTTPClient *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{
		ApiKey:     apiKey,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

type baixarXmlsRequest struct {
	XmlType        XmlType `json:"XmlType"`
	Take           int     `json:"take"` // máx 50 por request
	Skip           int     `json:"skip"`
	DataEmissaoIni string  `json:"DataEmissaoInicio"` // "2026-08-01"
	DataEmissaoFim string  `json:"DataEmissaoFim"`
}

// XmlResult é um XML individual devolvido pela API (o payload exato ainda
// precisa ser confirmado contra a resposta real — a doc pública não detalha
// 100% dos campos do retorno; ajustar quando testarmos com a API key real).
type XmlResult struct {
	Chave     string `json:"chave"`
	XmlBase64 string `json:"xml"` // supostamente base64; confirmar
}

// BaixarXmls pede um lote de XMLs de um tipo, num intervalo de datas.
// Limite documentado: 50 por request, 30 requests/minuto.
func (c *Client) BaixarXmls(tipo XmlType, dataIni, dataFim time.Time, skip int) ([]XmlResult, error) {
	reqBody := baixarXmlsRequest{
		XmlType:        tipo,
		Take:           50,
		Skip:           skip,
		DataEmissaoIni: dataIni.Format("2006-01-02"),
		DataEmissaoFim: dataFim.Format("2006-01-02"),
	}

	buf, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("montar request SIEG: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, baixarXmlsURL+"?apikey="+c.ApiKey, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("criar request SIEG: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chamar API SIEG: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SIEG retornou status %d", resp.StatusCode)
	}

	var results []XmlResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decodificar resposta SIEG: %w", err)
	}

	return results, nil
}

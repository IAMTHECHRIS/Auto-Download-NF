// Package adn fala com a API do Ambiente de Dados Nacional (Sistema
// Nacional NFS-e) — distribuição de documentos por NSU (Número Sequencial
// Único), autenticada por certificado digital A1/A3 (mTLS, sem token).
package adn

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"io-nf-automation/internal/certload"
)

const baseURLProducao = "https://adn.nfse.gov.br/contribuintes"

// Client fala com a API ADN usando mTLS (o certificado É a autenticação —
// sem usuário/senha/token separado).
type Client struct {
	baseURL    string
	HTTPClient *http.Client
}

// NewClient monta um client autenticado direto a partir do .pfx original —
// sem depender de OpenSSL nem de extrair .pem antes. Funciona igual em
// Linux e Windows.
func NewClient(caminhoPfx, senhaPfx string) (*Client, error) {
	cert, err := certload.FromPFXValidado(caminhoPfx, senhaPfx)
	if err != nil {
		return nil, fmt.Errorf("carregar certificado: %w", err)
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
		},
	}

	return &Client{
		baseURL: baseURLProducao,
		HTTPClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
	}, nil
}

// DFeItem é um documento individual dentro do lote retornado por /DFe/{NSU}.
type DFeItem struct {
	NSU             int    `json:"NSU"`
	ChaveAcesso     string `json:"ChaveAcesso"`
	TipoDocumento   string `json:"TipoDocumento"`
	ArquivoXml      string `json:"ArquivoXml"` // base64(gzip(xml))
	DataHoraGeracao string `json:"DataHoraGeracao"`
}

// DFeResponse é a resposta completa de /DFe/{NSU}.
type DFeResponse struct {
	StatusProcessamento string    `json:"StatusProcessamento"`
	LoteDFe             []DFeItem `json:"LoteDFe"`
	Alertas             []string  `json:"Alertas"`
	Erros               []string  `json:"Erros"`
	TipoAmbiente        string    `json:"TipoAmbiente"`
}

// BuscarLote pede o lote de documentos a partir do NSU informado. A API
// devolve até 50 documentos por chamada; pra continuar, chame de novo com
// o maior NSU do lote anterior. Faz retry com backoff em caso de 429 (rate
// limit) — a API não documenta o limite exato, então esperamos progressivo.
func (c *Client) BuscarLote(nsu int) (DFeResponse, error) {
	url := fmt.Sprintf("%s/DFe/%d", c.baseURL, nsu)

	const maxTentativas = 5
	backoff := 3 * time.Second

	for tentativa := 1; tentativa <= maxTentativas; tentativa++ {
		resp, err := c.HTTPClient.Get(url)
		if err != nil {
			return DFeResponse{}, fmt.Errorf("chamar ADN: %w", err)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			if tentativa == maxTentativas {
				return DFeResponse{}, fmt.Errorf("ADN: rate limit persistente após %d tentativas", maxTentativas)
			}
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return DFeResponse{}, fmt.Errorf("ADN retornou status %d: %s", resp.StatusCode, body)
		}

		var out DFeResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return DFeResponse{}, fmt.Errorf("decodificar resposta ADN: %w", err)
		}

		return out, nil
	}

	return DFeResponse{}, fmt.Errorf("BuscarLote: não deveria chegar aqui")
}

// DecodeXML desfaz o base64+gzip do campo ArquivoXml e devolve o XML puro.
func DecodeXML(arquivoXmlBase64 string) ([]byte, error) {
	compressed, err := base64.StdEncoding.DecodeString(arquivoXmlBase64)
	if err != nil {
		return nil, fmt.Errorf("decodificar base64: %w", err)
	}

	r, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("abrir gzip: %w", err)
	}
	defer r.Close()

	xmlBytes, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("descomprimir gzip: %w", err)
	}

	return xmlBytes, nil
}

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
	"net/url"
	"strings"
	"time"

	"io-nf-automation/internal/certload"
)

const baseURLProducao = "https://adn.nfse.gov.br/contribuintes"

// baseURLHomologacao — a SEFAZ chama esse ambiente de "produção restrita",
// mas é o equivalente funcional de homologação: dado fictício, sem contar
// pra cota real.
const baseURLHomologacao = "https://adn.producaorestrita.nfse.gov.br/contribuintes"

// Client fala com a API ADN usando mTLS (o certificado É a autenticação —
// sem usuário/senha/token separado).
type Client struct {
	baseURL    string
	HTTPClient *http.Client
	cert       tls.Certificate
	pfxPath    string
	pfxSenha   string
}

// NewClient monta um client autenticado direto a partir do .pfx original —
// sem depender de OpenSSL nem de extrair .pem antes. Funciona igual em
// Linux e Windows. tpAmb é "1" (produção) ou "2" (homologação) — ver
// appconfig.Config.TpAmb().
func NewClient(caminhoPfx, senhaPfx, tpAmb string) (*Client, error) {
	cert, err := certload.FromPFXValidado(caminhoPfx, senhaPfx)
	if err != nil {
		return nil, fmt.Errorf("carregar certificado: %w", err)
	}

	transport := &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		ForceAttemptHTTP2: false,
		TLSNextProto:      map[string]func(string, *tls.Conn) http.RoundTripper{},
		TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
	}

	baseURL := baseURLProducao
	if tpAmb == "2" {
		baseURL = baseURLHomologacao
	}

	return &Client{
		baseURL:  baseURL,
		cert:     cert,
		pfxPath:  caminhoPfx,
		pfxSenha: senhaPfx,
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
	Alertas             mensagens `json:"Alertas"`
	Erros               mensagens `json:"Erros"`
	TipoAmbiente        string    `json:"TipoAmbiente"`
}

type mensagens []string

func (m *mensagens) UnmarshalJSON(data []byte) error {
	var textos []string
	if err := json.Unmarshal(data, &textos); err == nil {
		*m = textos
		return nil
	}

	var objetos []struct {
		Codigo    string          `json:"Codigo"`
		Descricao string          `json:"Descricao"`
		Mensagem  json.RawMessage `json:"Mensagem"`
	}
	if err := json.Unmarshal(data, &objetos); err != nil {
		return err
	}

	out := make([]string, 0, len(objetos))
	for _, obj := range objetos {
		switch {
		case obj.Codigo != "" && obj.Descricao != "":
			out = append(out, obj.Codigo+": "+obj.Descricao)
		case obj.Descricao != "":
			out = append(out, obj.Descricao)
		case obj.Codigo != "":
			out = append(out, obj.Codigo)
		case len(obj.Mensagem) > 0 && string(obj.Mensagem) != "{}":
			out = append(out, string(obj.Mensagem))
		}
	}
	*m = out
	return nil
}

// BuscarLote pede o lote de documentos a partir do NSU informado. A API
// devolve até 50 documentos por chamada; pra continuar, chame de novo com
// o maior NSU do lote anterior. Faz retry com backoff só em caso de 429
// (rate limit) — quando a falha é TLS/handshake/conexão, parar na primeira
// tentativa é mais seguro: a ADN nem devolveu JSON, e insistir pode confundir
// operação e diagnóstico.
func (c *Client) BuscarLote(nsu int) (DFeResponse, error) {
	endpoint := fmt.Sprintf("%s/DFe/%d", c.baseURL, nsu)
	params := url.Values{}
	params.Set("tipoNSU", "DISTRIBUICAO")
	params.Set("lote", "true")
	endpoint += "?" + params.Encode()

	const maxTentativas429 = 5
	backoff := 3 * time.Second

	for tentativa := 1; tentativa <= maxTentativas429; tentativa++ {
		body, statusCode, err := c.getComFallback(endpoint)
		if err != nil {
			if tentativa < 3 && erroComunicacaoTLS(err) {
				time.Sleep(time.Duration(tentativa) * 2 * time.Second)
				continue
			}
			return DFeResponse{}, err
		}

		if statusCode == http.StatusTooManyRequests {
			if tentativa == maxTentativas429 {
				return DFeResponse{}, fmt.Errorf("ADN: rate limit persistente após %d tentativas", maxTentativas429)
			}
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		var out DFeResponse
		if len(body) > 0 {
			if err := json.Unmarshal(body, &out); err != nil {
				return DFeResponse{}, fmt.Errorf("decodificar resposta ADN: %w; status=%d; trecho=%q", err, statusCode, trechoResposta(body, 500))
			}
		}

		if statusCode != http.StatusOK {
			if statusCode == http.StatusNotFound && out.StatusProcessamento == "NENHUM_DOCUMENTO_LOCALIZADO" {
				return out, nil
			}
			return DFeResponse{}, fmt.Errorf("ADN retornou status %d: %s", statusCode, body)
		}

		return out, nil
	}

	return DFeResponse{}, fmt.Errorf("BuscarLote: não deveria chegar aqui")
}

func trechoResposta(body []byte, limite int) string {
	texto := strings.TrimSpace(string(body))
	if len(texto) <= limite {
		return texto
	}
	return texto[:limite] + "..."
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

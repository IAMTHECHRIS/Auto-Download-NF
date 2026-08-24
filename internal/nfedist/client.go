// Package nfedist fala com o webservice oficial NFeDistribuicaoDFe (SEFAZ,
// Ambiente Nacional) — distribuição de NFe/CTe por NSU, gratuito, mesmo
// padrão de autenticação por certificado A1 (mTLS) que a NFSe.
//
// Referência: Nota Técnica 2014/002, WSDL em
// https://www1.nfe.fazenda.gov.br/NFeDistribuicaoDFe/NFeDistribuicaoDFe.asmx?wsdl
package nfedist

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"encoding/base64"
	"encoding/xml"
	"fmt"

	"io"
	"net/http"
	"sieg-automation/internal/certload"
	"time"
)

const urlProducao = "https://www1.nfe.fazenda.gov.br/NFeDistribuicaoDFe/NFeDistribuicaoDFe.asmx"
const soapAction = "http://www.portalfiscal.inf.br/nfe/wsdl/NFeDistribuicaoDFe/nfeDistDFeInteresse"

type Client struct {
	HTTPClient *http.Client
}

// NewClient monta um client autenticado a partir de um certificado .pfx
// (senha em texto). Funciona igual em Linux e Windows — não depende de
// OpenSSL nem de arquivo .pem pré-extraído.
func NewClient(caminhoPfx, senhaPfx string) (*Client, error) {
	cert, err := certload.FromPFXValidado(caminhoPfx, senhaPfx)
	if err != nil {
		return nil, fmt.Errorf("carregar certificado: %w", err)
	}
	return &Client{
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					Certificates: []tls.Certificate{cert},
					// o webservice da SEFAZ (legado, .NET antigo) pede
					// renegociação TLS durante o handshake — o Go desativa
					// isso por padrão, precisa habilitar explicitamente.
					Renegotiation: tls.RenegotiateFreelyAsClient,
				},
			},
		},
	}, nil
}

// envelope SOAP 1.1 — request
const envelopeTemplate = `<?xml version="1.0" encoding="utf-8"?>
<soap:Envelope xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body>
    <nfeDistDFeInteresse xmlns="http://www.portalfiscal.inf.br/nfe/wsdl/NFeDistribuicaoDFe">
      <nfeDadosMsg>
        <distDFeInt xmlns="http://www.portalfiscal.inf.br/nfe" versao="1.01">
          <tpAmb>1</tpAmb>
          <cUFAutor>%s</cUFAutor>
          <CNPJ>%s</CNPJ>
          <distNSU>
            <ultNSU>%015d</ultNSU>
          </distNSU>
        </distDFeInt>
      </nfeDadosMsg>
    </nfeDistDFeInteresse>
  </soap:Body>
</soap:Envelope>`

// --- Resposta ---

type retDistDFeInt struct {
	XMLName        xml.Name `xml:"retDistDFeInt"`
	TpAmb          string   `xml:"tpAmb"`
	VerAplic       string   `xml:"verAplic"`
	CStat          string   `xml:"cStat"`
	XMotivo        string   `xml:"xMotivo"`
	UltNSU         string   `xml:"ultNSU"`
	MaxNSU         string   `xml:"maxNSU"`
	LoteDistDFeInt struct {
		DocZip []DocZip `xml:"docZip"`
	} `xml:"loteDistDFeInt"`
}

// DocZip é um documento individual do lote — o conteúdo vem em base64+gzip,
// igual ao padrão que já vimos na NFSe (ArquivoXml).
type DocZip struct {
	NSU    string `xml:"NSU,attr"`
	Schema string `xml:"schema,attr"` // ex: "resNFe_v1.01.xsd", "procNFe_v4.00.xsd", "resEvento_v1.01.xsd"
	Data   string `xml:",chardata"`
}

// Resultado é o que BuscarLote devolve — já pronto pra iterar.
type Resultado struct {
	CStat   string
	XMotivo string
	UltNSU  string
	MaxNSU  string
	Docs    []DocZip
}

// BuscarLote consulta a partir do ultNSU informado (0 = do início, só a
// PRIMEIRA vez). cUFAutor é o código IBGE do estado (SP = 35).
//
// ARMADILHA CONHECIDA (descoberta em 2026-08-23): essa API tem controle
// anti-abuso rígido — depois da primeira consulta, é OBRIGATÓRIO continuar
// do ultNSU retornado. Reiniciar do 0 repetidamente derruba cStat=656
// ("Consumo Indevido") com bloqueio de 1 HORA. Diferente da API da NFSe
// (que tolera reiniciar do 0 sempre). Quem chama isso precisa persistir o
// último NSU em disco entre execuções — ver checkpoint.go.
func (c *Client) BuscarLote(cUFAutor, cnpj string, ultNSU int) (Resultado, error) {
	body := fmt.Sprintf(envelopeTemplate, cUFAutor, cnpj, ultNSU)

	req, err := http.NewRequest(http.MethodPost, urlProducao, bytes.NewBufferString(body))
	if err != nil {
		return Resultado{}, fmt.Errorf("criar request: %w", err)
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", soapAction)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return Resultado{}, fmt.Errorf("chamar NFeDistribuicaoDFe: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Resultado{}, fmt.Errorf("ler resposta: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return Resultado{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// a resposta vem dentro de um SOAP envelope; extrai só o retDistDFeInt
	var envelope struct {
		Body struct {
			Response struct {
				Result struct {
					RetDistDFeInt retDistDFeInt `xml:"retDistDFeInt"`
				} `xml:"nfeDistDFeInteresseResult"`
			} `xml:"nfeDistDFeInteresseResponse"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal(respBody, &envelope); err != nil {
		return Resultado{}, fmt.Errorf("parse envelope SOAP: %w — corpo bruto: %s", err, string(respBody[:min(2000, len(respBody))]))
	}

	ret := envelope.Body.Response.Result.RetDistDFeInt
	return Resultado{
		CStat:   ret.CStat,
		XMotivo: ret.XMotivo,
		UltNSU:  ret.UltNSU,
		MaxNSU:  ret.MaxNSU,
		Docs:    ret.LoteDistDFeInt.DocZip,
	}, nil
}

// DecodeXML desfaz o base64+gzip de um DocZip.
func DecodeXML(d DocZip) ([]byte, error) {
	compressed, err := base64.StdEncoding.DecodeString(d.Data)
	if err != nil {
		return nil, fmt.Errorf("decodificar base64: %w", err)
	}
	r, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("abrir gzip: %w", err)
	}
	defer r.Close()
	return io.ReadAll(r)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

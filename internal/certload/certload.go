// Package certload carrega o certificado A1 direto do arquivo .pfx original
// — sem precisar de OpenSSL nem de extrair .pem antes. Usa uma biblioteca
// Go pura (software.sslmate.com/src/go-pkcs12), que já lida com o
// algoritmo legado (RC2-40-CBC) que esse tipo de certificado costuma usar,
// sem exigir flag "-legacy" nem nada instalado na máquina. Funciona igual
// no Linux e no Windows.
package certload

import (
	"crypto/tls"
	"fmt"
	"os"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// FromPFX lê um arquivo .pfx e devolve um tls.Certificate pronto pra usar
// em http.Transport / tls.Config — mesmo formato que tls.LoadX509KeyPair
// devolveria a partir de .pem.
func FromPFX(caminhoPfx, senha string) (tls.Certificate, error) {
	raw, err := os.ReadFile(caminhoPfx)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("ler %s: %w", caminhoPfx, err)
	}

	chave, cert, err := pkcs12.Decode(raw, senha)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("decodificar pfx (senha errada?): %w", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{cert.Raw},
		PrivateKey:  chave,
		Leaf:        cert,
	}, nil
}

// FromPFXValidado é igual FromPFX mas falha cedo, com mensagem clara, se o
// certificado já estiver vencido — em vez de deixar a chamada HTTP dar um
// erro TLS genérico e confuso na hora de conectar na SEFAZ.
func FromPFXValidado(caminhoPfx, senha string) (tls.Certificate, error) {
	cert, err := FromPFX(caminhoPfx, senha)
	if err != nil {
		return tls.Certificate{}, err
	}

	agora := time.Now()
	if agora.After(cert.Leaf.NotAfter) {
		return tls.Certificate{}, fmt.Errorf(
			"certificado VENCIDO em %s (hoje é %s) — troque o arquivo .pfx pelo certificado renovado",
			cert.Leaf.NotAfter.Format("02/01/2006"), agora.Format("02/01/2006"),
		)
	}
	if agora.Before(cert.Leaf.NotBefore) {
		return tls.Certificate{}, fmt.Errorf("certificado ainda não é válido (começa em %s)", cert.Leaf.NotBefore.Format("02/01/2006"))
	}

	return cert, nil
}

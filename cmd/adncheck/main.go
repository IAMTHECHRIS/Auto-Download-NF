package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"io-nf-automation/internal/certload"
)

func main() {
	pfx := flag.String("pfx", "", "caminho do certificado .pfx")
	senha := flag.String("senha", "", "senha do certificado")
	nsu := flag.Int("nsu", 0, "NSU para teste")
	base := flag.String("base", "https://adn.nfse.gov.br/contribuintes", "base URL ADN")
	query := flag.String("query", "", "query string opcional, ex: tipoNSU=DISTRIBUICAO&lote=true")
	tls12 := flag.Bool("tls12", false, "forçar TLS 1.2")
	flag.Parse()

	if *pfx == "" || *senha == "" {
		fmt.Fprintln(os.Stderr, "uso: adncheck -pfx arquivo.pfx -senha senha [-nsu 0] [-tls12]")
		os.Exit(2)
	}

	cert, err := certload.FromPFXValidado(*pfx, *senha)
	if err != nil {
		fmt.Fprintf(os.Stderr, "CERTIFICADO_ERRO: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("CERTIFICADO_OK: sujeito=%q valido_ate=%s\n", cert.Leaf.Subject.CommonName, cert.Leaf.NotAfter.Format("2006-01-02"))

	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	if *tls12 {
		tlsCfg.MaxVersion = tls.VersionTLS12
	}
	client := &http.Client{
		Timeout: 45 * time.Second,
		Transport: &http.Transport{
			Proxy:             http.ProxyFromEnvironment,
			ForceAttemptHTTP2: false,
			TLSNextProto:      map[string]func(string, *tls.Conn) http.RoundTripper{},
			TLSClientConfig:   tlsCfg,
		},
	}

	url := fmt.Sprintf("%s/DFe/%d", *base, *nsu)
	if strings.TrimSpace(*query) != "" {
		url += "?" + strings.TrimPrefix(strings.TrimSpace(*query), "?")
	}
	fmt.Printf("GET %s\n", url)
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "HTTP_ERRO: %T: %v\n", err, err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	fmt.Printf("HTTP_STATUS: %d %s\n", resp.StatusCode, resp.Status)
	fmt.Printf("TLS: versao=0x%x cipher=0x%x server=%q\n", resp.TLS.Version, resp.TLS.CipherSuite, resp.TLS.ServerName)
	fmt.Printf("BODY_INICIO:\n%s\n", string(body))
}

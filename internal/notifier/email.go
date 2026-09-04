package notifier

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"time"

	"io-nf-automation/internal/appconfig"
)

type Documento struct {
	Tipo       string
	Fornecedor string
	Numero     string
	Valor      float64
	CaminhoXML string
}

func EnviarNovosDocumentos(cfg appconfig.Config, docs []Documento) error {
	if !cfg.EmailAtivo || len(docs) == 0 {
		return nil
	}
	if strings.TrimSpace(cfg.EmailSMTP) == "" || cfg.EmailPorta == 0 ||
		strings.TrimSpace(cfg.EmailUsuario) == "" || strings.TrimSpace(cfg.EmailSenha) == "" ||
		strings.TrimSpace(cfg.EmailDe) == "" || strings.TrimSpace(cfg.EmailPara) == "" {
		return fmt.Errorf("e-mail ativo, mas configuração SMTP incompleta")
	}

	var anexos []string
	for _, d := range docs {
		if d.CaminhoXML != "" {
			if _, err := os.Stat(d.CaminhoXML); err == nil {
				anexos = append(anexos, d.CaminhoXML)
			}
			pdf := strings.TrimSuffix(d.CaminhoXML, filepath.Ext(d.CaminhoXML)) + ".pdf"
			if _, err := os.Stat(pdf); err == nil {
				anexos = append(anexos, pdf)
			}
		}
	}
	if len(anexos) == 0 {
		return nil
	}

	assunto := fmt.Sprintf("Novos documentos fiscais — %s", time.Now().Format("02/01/2006 15:04"))
	corpo := resumoTexto(docs, anexos)
	msg, err := montarMIME(cfg.EmailDe, cfg.EmailPara, assunto, corpo, anexos)
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(cfg.EmailSMTP, fmt.Sprint(cfg.EmailPorta))
	auth := smtp.PlainAuth("", cfg.EmailUsuario, cfg.EmailSenha, cfg.EmailSMTP)
	return enviarSMTP(addr, cfg.EmailSMTP, auth, cfg.EmailDe, []string{cfg.EmailPara}, msg)
}

func resumoTexto(docs []Documento, anexos []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Coleta de notas fiscais concluída.\r\n\r\n")
	fmt.Fprintf(&b, "Novos documentos no catálogo: %d\r\n", len(docs))
	fmt.Fprintf(&b, "Anexos enviados: %d\r\n\r\n", len(anexos))
	for _, d := range docs {
		fmt.Fprintf(&b, "- %s %s — %s — R$ %.2f\r\n", d.Tipo, d.Numero, d.Fornecedor, d.Valor)
	}
	fmt.Fprintf(&b, "\r\nObservação: PDF é anexado quando existir ao lado do XML. NFS-e ainda pode seguir somente com XML.\r\n")
	return b.String()
}

func montarMIME(de, para, assunto, corpo string, anexos []string) ([]byte, error) {
	boundary := fmt.Sprintf("io_nf_%d", time.Now().UnixNano())
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", de)
	fmt.Fprintf(&b, "To: %s\r\n", para)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", assunto))
	fmt.Fprintf(&b, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", boundary)

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	fmt.Fprintf(&b, "Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(&b, "Content-Transfer-Encoding: 8bit\r\n\r\n")
	fmt.Fprintf(&b, "%s\r\n", corpo)

	for _, caminho := range anexos {
		data, err := os.ReadFile(caminho)
		if err != nil {
			return nil, fmt.Errorf("ler anexo %s: %w", filepath.Base(caminho), err)
		}
		nome := filepath.Base(caminho)
		contentType := "application/octet-stream"
		if strings.EqualFold(filepath.Ext(caminho), ".xml") {
			contentType = "application/xml"
		} else if strings.EqualFold(filepath.Ext(caminho), ".pdf") {
			contentType = "application/pdf"
		}
		fmt.Fprintf(&b, "--%s\r\n", boundary)
		fmt.Fprintf(&b, "Content-Type: %s; name=%q\r\n", contentType, nome)
		fmt.Fprintf(&b, "Content-Disposition: attachment; filename=%q\r\n", nome)
		fmt.Fprintf(&b, "Content-Transfer-Encoding: base64\r\n\r\n")
		enc := base64.StdEncoding.EncodeToString(data)
		for len(enc) > 76 {
			fmt.Fprintf(&b, "%s\r\n", enc[:76])
			enc = enc[76:]
		}
		fmt.Fprintf(&b, "%s\r\n", enc)
	}

	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes(), nil
}

func enviarSMTP(addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("conectar SMTP: %w", err)
	}
	defer c.Close()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("STARTTLS SMTP: %w", err)
		}
	}
	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("autenticar SMTP: %w", err)
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("SMTP MAIL FROM: %w", err)
	}
	for _, dest := range to {
		if err := c.Rcpt(dest); err != nil {
			return fmt.Errorf("SMTP RCPT TO %s: %w", dest, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return fmt.Errorf("enviar corpo SMTP: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("finalizar SMTP DATA: %w", err)
	}
	return c.Quit()
}

// Comando de teste: gera um DANFE em PDF a partir de um XML de NFe já
// baixado, pra validar visualmente o layout sem precisar rodar o coletor
// inteiro. Uso: go run ./cmd/danfe-preview <xml> <pdf-saida>
package main

import (
	"fmt"
	"os"

	"io-nf-automation/internal/danfe"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "uso: danfe-preview <entrada.xml> <saida.pdf>")
		os.Exit(1)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ler xml:", err)
		os.Exit(1)
	}
	nfe, err := danfe.ParseNFe(raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse xml:", err)
		os.Exit(1)
	}
	out, err := os.Create(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "criar saida:", err)
		os.Exit(1)
	}
	defer out.Close()
	if err := danfe.Gerar(out, nfe, "I.O NF Automation"); err != nil {
		fmt.Fprintln(os.Stderr, "gerar pdf:", err)
		os.Exit(1)
	}
	fmt.Println("PDF gerado em", os.Args[2])
}

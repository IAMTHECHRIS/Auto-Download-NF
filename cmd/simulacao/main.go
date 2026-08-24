// Protótipo do pipeline de captura/organização de documentos fiscais.
//
// MODO ATUAL: simulação. Ainda não temos a API key do SIEG nem o certificado
// A1 renovado, então este main.go lê um XML sintético de testdata/ em vez de
// chamar a API real. Quando os dois estiverem disponíveis, troca a leitura
// do arquivo local pela chamada real em internal/sieg.Client.BaixarXmls —
// o resto do pipeline (parse → renomeio → organização de pasta) já funciona
// igual, independente da fonte.
package main

import (
	"fmt"
	"log"
	"os"

	"sieg-automation/internal/document"
	"sieg-automation/internal/organizer"
)

func main() {
	fmt.Println("=== Pipeline de captura fiscal — modo SIMULAÇÃO ===")
	fmt.Println("(sem API key do SIEG nem certificado renovado ainda)")
	fmt.Println()

	raw, err := os.ReadFile("testdata/nfe_exemplo_sintetico.xml")
	if err != nil {
		log.Fatalf("ler XML de teste: %v", err)
	}

	doc, err := document.ParseNFe(raw)
	if err != nil {
		log.Fatalf("parse XML: %v", err)
	}

	fmt.Printf("Documento extraído:\n")
	fmt.Printf("  Tipo:       %s\n", doc.Tipo)
	fmt.Printf("  Fornecedor: %s\n", doc.Fornecedor)
	fmt.Printf("  Data:       %s\n", doc.Data.Format("2006-01-02"))
	fmt.Printf("  Número:     %s\n", doc.Numero)
	fmt.Printf("  Valor:      R$ %.2f\n", doc.Valor)
	fmt.Println()

	nome := organizer.FileName(doc)
	fmt.Printf("Nome de arquivo gerado: %s\n", nome)

	pasta := organizer.FolderPath("./saida", doc)
	fmt.Printf("Pasta de destino:       %s\n", pasta)
	fmt.Println()

	caminho, err := organizer.PlaceDocument("./saida", doc, ".xml", raw)
	if err != nil {
		log.Fatalf("gravar documento: %v", err)
	}
	fmt.Printf("Arquivo gravado em: %s\n", caminho)
}

// Teste offline do pipeline de parse/renomeio/organização de documentos
// fiscais — lê um XML sintético de testdata/ em vez de chamar os
// webservices reais (nfedist/adn), útil pra testar essa parte sem precisar
// de certificado nem rede. O pipeline de coleta de verdade fica em
// cmd/coletor.
package main

import (
	"fmt"
	"log"
	"os"

	"io-nf-automation/internal/document"
	"io-nf-automation/internal/organizer"
)

func main() {
	fmt.Println("=== Pipeline de captura fiscal — teste offline (XML sintético) ===")
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

package document

import "testing"

func TestParseNFSeNacionalNormalizaCNPJParaDirecao(t *testing.T) {
	raw := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<NFSe>
  <infNFSe>
    <nNFSe>123</nNFSe>
    <dhProc>2026-09-01T10:00:00Z</dhProc>
    <emit>
      <CNPJ>11.111.111/0001-11</CNPJ>
      <xNome>Prestador Teste</xNome>
    </emit>
    <valores>
      <vLiq>150.75</vLiq>
    </valores>
    <DPS>
      <infDPS>
        <dCompet>2026-09-01</dCompet>
        <toma>
          <CNPJ>08.419.940/0001-21</CNPJ>
          <xNome>Thesis Engenharia</xNome>
        </toma>
      </infDPS>
    </DPS>
  </infNFSe>
</NFSe>`)

	doc, direcao, err := ParseNFSeNacional(raw, "08419940000121")
	if err != nil {
		t.Fatalf("ParseNFSeNacional() erro = %v", err)
	}
	if direcao != DirecaoRecebida {
		t.Fatalf("direcao = %q, quero %q", direcao, DirecaoRecebida)
	}
	if doc.FornecedorDoc != "11111111000111" {
		t.Fatalf("FornecedorDoc = %q, quero somente dígitos", doc.FornecedorDoc)
	}
}

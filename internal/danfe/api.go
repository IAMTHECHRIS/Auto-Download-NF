package danfe

import "bytes"

// GerarDeXML monta o DANFE em PDF direto a partir dos bytes do XML já salvo
// (nfeProc) — conveniência pro pipeline de coleta, que só tem os bytes em
// mãos, não um io.Writer.
func GerarDeXML(xmlBytes []byte, gerador string) ([]byte, error) {
	nfe, err := ParseNFe(xmlBytes)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := Gerar(&buf, nfe, gerador); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Package catalogo mantém um registro (append-only) de cada documento
// baixado, num CSV na pasta de saída. Existe pra alimentar o painel do
// programa — uma lista consultável do que já foi processado — sem depender
// de escanear as pastas ANO/MES/TIPO toda vez.
//
// IMPORTANTE: apagar uma linha ou o arquivo inteiro não faz o coletor
// baixar de novo — a API da SEFAZ não permite voltar atrás no NSU (ver
// coletanfe/coletanfe.go). O catálogo é só um índice de leitura, não um
// gatilho de download.
package catalogo

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"io-nf-automation/internal/appconfig"
	"io-nf-automation/internal/document"
)

// Entrada é uma linha do catálogo já decodificada, pronta pro painel exibir.
type Entrada struct {
	RegistradoEm  time.Time `json:"registrado_em"`
	Tipo          string    `json:"tipo"`
	Fornecedor    string    `json:"fornecedor"`
	FornecedorDoc string    `json:"fornecedor_doc"` // CPF/CNPJ separado do nome — ver splitFornecedor
	Data          time.Time `json:"data"`
	Numero        string    `json:"numero"`
	Valor         float64   `json:"valor"`
	Status        string    `json:"status"`
	Chave         string    `json:"chave"`
	Caminho       string    `json:"caminho"`
	TemPDF        bool      `json:"tem_pdf"`
}

// reFornecedorComDoc reconhece o CPF PARCIAL (só 8 dígitos, ##.###.###,
// mascarado por LGPD — os 3 últimos dígitos não vêm) que a NFS-e do
// Sistema Nacional devolve GRUDADO no nome quando o prestador é pessoa
// física/MEI, ex: "62.108.173 MARCIA DOS SANTOS". NFe de compra (empresa
// com CNPJ completo) não tem esse problema — o xNome já vem limpo.
var reFornecedorComDoc = regexp.MustCompile(`^(\d{2}\.\d{3}\.\d{3})\s+(.+)$`)

// splitFornecedor separa o documento mascarado do nome, se existir —
// puramente cosmético, não altera o que está gravado no CSV. Existe
// porque ordenar/filtrar por "fornecedor" fica quebrado com o número
// colado na frente (agrupa por dígito em vez de por nome).
// formatarDoc põe a máscara no CNPJ (14 dígitos) ou CPF (11) que vem só com
// números do XML. Qualquer outro tamanho volta como veio.
func formatarDoc(doc string) string {
	switch len(doc) {
	case 14:
		return fmt.Sprintf("%s.%s.%s/%s-%s", doc[0:2], doc[2:5], doc[5:8], doc[8:12], doc[12:])
	case 11:
		return fmt.Sprintf("%s.%s.%s-%s", doc[0:3], doc[3:6], doc[6:9], doc[9:])
	}
	return doc
}

func splitFornecedor(fornecedor string) (nome, doc string) {
	if m := reFornecedorComDoc.FindStringSubmatch(fornecedor); m != nil {
		return m[2], m[1]
	}
	return fornecedor, ""
}

func caminhoCSV(pastaSaida string) string {
	return filepath.Join(appconfig.PastaControle(pastaSaida), "catalogo.csv")
}

// Registrar acrescenta uma linha nova — nunca reescreve o arquivo inteiro.
// Quando o mesmo documento aparece de novo (ex: evento de cancelamento
// atualizando o status), entra uma linha NOVA; Listar() decide qual delas
// mostrar (a mais recente por chave), sem precisar editar a linha antiga.
func Registrar(pastaSaida string, d document.Document, caminho string) error {
	path := caminhoCSV(pastaSaida)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("criar pasta _Controle: %w", err)
	}
	_, err := os.Stat(path)
	novo := os.IsNotExist(err)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("abrir catálogo: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	if novo {
		if err := w.Write([]string{"registrado_em", "tipo", "fornecedor", "data", "numero", "valor", "status", "chave", "caminho", "fornecedor_doc"}); err != nil {
			return fmt.Errorf("gravar cabeçalho do catálogo: %w", err)
		}
	}

	// fornecedor_doc entra no FIM da linha de propósito: catálogo antigo
	// (9 colunas) continua sendo lido sem quebrar — Listar trata a coluna
	// extra como opcional.
	err = w.Write([]string{
		time.Now().Format(time.RFC3339),
		string(d.Tipo),
		d.Fornecedor,
		d.Data.Format("2006-01-02"),
		d.Numero,
		strconv.FormatFloat(d.Valor, 'f', 2, 64),
		d.Status,
		d.Chave,
		caminho,
		d.FornecedorDoc,
	})
	if err != nil {
		return fmt.Errorf("gravar linha do catálogo: %w", err)
	}
	return nil
}

// Listar lê o catálogo inteiro e devolve uma entrada por chave de acesso —
// a mais recente (assim um cancelamento, que gera uma linha nova, aparece
// atualizado na lista sem precisar editar/apagar a linha antiga).
// Documentos sem chave (raro — eventos órfãos) entram todos, sem dedupe.
func Listar(pastaSaida string) ([]Entrada, error) {
	f, err := os.Open(caminhoCSV(pastaSaida))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("abrir catálogo: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	linhas, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("ler catálogo: %w", err)
	}
	if len(linhas) <= 1 {
		return nil, nil
	}

	porChave := make(map[string]Entrada)
	var semChave []Entrada

	for _, linha := range linhas[1:] {
		if len(linha) < 9 {
			continue
		}
		registradoEm, _ := time.Parse(time.RFC3339, linha[0])
		data, _ := time.Parse("2006-01-02", linha[3])
		valor, _ := strconv.ParseFloat(linha[5], 64)

		e := Entrada{
			RegistradoEm: registradoEm,
			Tipo:         linha[1],
			Data:         data,
			Numero:       linha[4],
			Valor:        valor,
			Status:       linha[6],
			Chave:        linha[7],
			Caminho:      linha[8],
		}
		e.Fornecedor, e.FornecedorDoc = splitFornecedor(linha[2])
		// coluna 9 (fornecedor_doc) só existe em catálogo gravado a partir
		// da versão que passou a capturar CNPJ do XML — quando existe, ela
		// manda (é o dado real, não o extraído do nome).
		if len(linha) >= 10 && linha[9] != "" {
			e.FornecedorDoc = formatarDoc(linha[9])
		}
		if e.Caminho != "" {
			pdfPath := strings.TrimSuffix(e.Caminho, filepath.Ext(e.Caminho)) + ".pdf"
			if _, err := os.Stat(pdfPath); err == nil {
				e.TemPDF = true
			}
		}

		if e.Chave == "" {
			semChave = append(semChave, e)
			continue
		}
		porChave[e.Chave] = e
	}

	out := make([]Entrada, 0, len(porChave)+len(semChave))
	for _, e := range porChave {
		out = append(out, e)
	}
	out = append(out, semChave...)

	sort.Slice(out, func(i, j int) bool {
		return out[i].Data.After(out[j].Data)
	})

	return out, nil
}

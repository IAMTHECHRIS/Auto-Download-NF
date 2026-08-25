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
	"sort"
	"strconv"
	"time"

	"sieg-automation/internal/appconfig"
	"sieg-automation/internal/document"
)

// Entrada é uma linha do catálogo já decodificada, pronta pro painel exibir.
type Entrada struct {
	RegistradoEm time.Time `json:"registrado_em"`
	Tipo         string    `json:"tipo"`
	Fornecedor   string    `json:"fornecedor"`
	Data         time.Time `json:"data"`
	Numero       string    `json:"numero"`
	Valor        float64   `json:"valor"`
	Status       string    `json:"status"`
	Chave        string    `json:"chave"`
	Caminho      string    `json:"caminho"`
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
		if err := w.Write([]string{"registrado_em", "tipo", "fornecedor", "data", "numero", "valor", "status", "chave", "caminho"}); err != nil {
			return fmt.Errorf("gravar cabeçalho do catálogo: %w", err)
		}
	}

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
			Fornecedor:   linha[2],
			Data:         data,
			Numero:       linha[4],
			Valor:        valor,
			Status:       linha[6],
			Chave:        linha[7],
			Caminho:      linha[8],
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

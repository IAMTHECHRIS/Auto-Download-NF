// Package verificacao compara o catálogo do que já foi baixado com o que
// realmente existe na pasta de destino (a pasta sincronizada de verdade,
// ex: dentro do Google Drive, pra onde o usuário copia manualmente os
// arquivos depois de conferir).
//
// IMPORTANTE: isso é só um checador informativo. Como o fluxo normal é
// COPIAR pra pasta de destino (o original continua na pasta de chegada),
// tudo que aparecer aqui como "faltando" é candidato real a "esqueci de
// copiar" — mas se o usuário mudar de ideia e passar a MOVER em vez de
// copiar, a lista passa a incluir também tudo que foi movido de propósito
// (não tem como o programa saber a diferença só olhando o disco).
package verificacao

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"sieg-automation/internal/catalogo"
)

// Faltando é uma nota que está no catálogo mas cujo arquivo não foi
// encontrado (por nome) em nenhum lugar dentro da pasta de destino.
type Faltando struct {
	Tipo       string    `json:"tipo"`
	Fornecedor string    `json:"fornecedor"`
	Data       time.Time `json:"data"`
	Numero     string    `json:"numero"`
	Valor      float64   `json:"valor"`
	Chave      string    `json:"chave"`
	Caminho    string    `json:"caminho"` // caminho original, na pasta de chegada
}

// Verificar varre pastaDestino (recursivamente) coletando todo nome de
// arquivo presente, e devolve as entradas do catálogo cujo nome de arquivo
// não apareceu em lugar nenhum lá dentro. Comparação é só pelo NOME do
// arquivo (não pelo conteúdo) — funciona bem porque o nome já é
// determinístico (fornecedor+data+tipo+número+valor).
func Verificar(pastaSaida, pastaDestino string) ([]Faltando, error) {
	if strings.TrimSpace(pastaDestino) == "" {
		return nil, fmt.Errorf("pasta de destino não configurada — defina ela na aba Configuração antes de verificar")
	}

	entradas, err := catalogo.Listar(pastaSaida)
	if err != nil {
		return nil, fmt.Errorf("ler catálogo: %w", err)
	}

	presentes := make(map[string]bool)
	err = filepath.WalkDir(pastaDestino, func(caminho string, d fs.DirEntry, err error) error {
		if err != nil {
			// pasta/arquivo pontual sem permissão de leitura, por exemplo —
			// pula e continua, não aborta a verificação inteira por isso.
			return nil
		}
		if !d.IsDir() {
			presentes[strings.ToLower(d.Name())] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("varrer pasta de destino: %w", err)
	}

	var faltando []Faltando
	for _, e := range entradas {
		// notas já marcadas CANCELADO não interessam pro controle de "cópia
		// pendente" — não faz sentido cobrar cópia de algo cancelado.
		if strings.EqualFold(e.Status, "CANCELADO") {
			continue
		}
		nome := strings.ToLower(filepath.Base(e.Caminho))
		if presentes[nome] {
			continue
		}
		faltando = append(faltando, Faltando{
			Tipo:       e.Tipo,
			Fornecedor: e.Fornecedor,
			Data:       e.Data,
			Numero:     e.Numero,
			Valor:      e.Valor,
			Chave:      e.Chave,
			Caminho:    e.Caminho,
		})
	}

	sort.Slice(faltando, func(i, j int) bool {
		return faltando[i].Data.After(faltando[j].Data)
	})

	return faltando, nil
}

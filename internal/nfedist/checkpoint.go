package nfedist

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// LerCheckpoint lê o último NSU processado de um arquivo. Devolve 0 se o
// arquivo não existir ainda (primeira execução).
func LerCheckpoint(caminho string) (int, error) {
	data, err := os.ReadFile(caminho)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("ler checkpoint %s: %w", caminho, err)
	}

	nsu, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("checkpoint %s com conteúdo inválido: %w", caminho, err)
	}
	return nsu, nil
}

// SalvarCheckpoint grava o último NSU processado — SEMPRE chamar isso
// depois de cada BuscarLote bem-sucedido, mesmo que dê erro no meio do
// processamento dos documentos. Nunca reiniciar do zero depois da primeira
// consulta (ver aviso em BuscarLote).
func SalvarCheckpoint(caminho string, nsu int) error {
	return os.WriteFile(caminho, []byte(strconv.Itoa(nsu)), 0o600)
}

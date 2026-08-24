// Package organizer monta o nome de arquivo padronizado e o caminho de pasta
// (ANO/MES/TIPO), sempre com base na DATA DO DOCUMENTO — não na data em que o
// processo rodou.
package organizer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sieg-automation/internal/document"
)

// FileName monta o padrão definido por Ismael:
// NOME FORNECEDOR_DATA_TIPO NUMERO_R$ VALOR_OP
// Exemplo: ISMAEL OLIVEIRA_260823_NFES 90_R$ 1.000,00_OP 72
// O campo OP é omitido quando vazio (custo da empresa, sem obra vinculada).
func FileName(d document.Document) string {
	data := d.Data.Format("060102") // YYMMDD
	valor := formatBRL(d.Valor)

	parts := []string{
		strings.ToUpper(strings.TrimSpace(d.Fornecedor)),
		data,
		fmt.Sprintf("%s %s", d.Tipo, d.Numero),
		fmt.Sprintf("R$ %s", valor),
	}
	if d.OP != "" {
		parts = append(parts, "OP "+d.OP)
	}
	if d.Status != "" {
		parts = append(parts, "STATUS "+strings.ToUpper(d.Status))
	}

	return strings.Join(parts, "_")
}

// formatBRL formata 1000.5 como "1.000,50" (separador de milhar e decimal
// no padrão brasileiro).
func formatBRL(v float64) string {
	inteiro := int64(v)
	centavos := int64((v-float64(inteiro))*100 + 0.5)

	s := fmt.Sprintf("%d", inteiro)
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, c)
	}

	return fmt.Sprintf("%s,%02d", out, centavos)
}

// FolderPath monta ANO/MES/TIPO com base na data do documento (não na data
// de hoje), ex: 2026/09/NFES.
func FolderPath(baseDir string, d document.Document) string {
	ano := d.Data.Format("2006")
	mes := d.Data.Format("01")
	return filepath.Join(baseDir, ano, mes, string(d.Tipo))
}

// PlaceDocument cria a árvore de pastas se não existir e grava o arquivo
// (ext inclui o ponto, ex: ".pdf" ou ".xml"). NUNCA sobrescreve um arquivo
// existente — se o nome colidir (dois documentos distintos com mesmo
// fornecedor/data/número/valor, ex: nota reemitida com o mesmo número após
// cancelamento), desambigua acrescentando os últimos dígitos da chave de
// acesso. Colisão real (mesmo conteúdo) é bug de dado — melhor um nome feio
// do que perder um documento fiscal.
func PlaceDocument(baseDir string, d document.Document, ext string, content []byte) (string, error) {
	dir := FolderPath(baseDir, d)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("criar pasta %s: %w", dir, err)
	}

	nome := FileName(d)
	fullPath := filepath.Join(dir, nome+ext)

	if _, err := os.Stat(fullPath); err == nil {
		// colisão — desambigua com os últimos 6 dígitos da chave (se tiver)
		// ou um contador, se a chave também não resolver.
		sufixo := d.Chave
		if len(sufixo) > 6 {
			sufixo = sufixo[len(sufixo)-6:]
		}
		if sufixo != "" {
			fullPath = filepath.Join(dir, nome+"_chave-"+sufixo+ext)
		}
		for i := 2; ; i++ {
			if _, err := os.Stat(fullPath); err != nil {
				break
			}
			fullPath = filepath.Join(dir, fmt.Sprintf("%s_dup%d%s", nome, i, ext))
		}
	}

	if err := os.WriteFile(fullPath, content, 0o644); err != nil {
		return "", fmt.Errorf("gravar arquivo %s: %w", fullPath, err)
	}

	return fullPath, nil
}

// MarcarCancelado renomeia o arquivo já gravado da nota original,
// adicionando a tag STATUS CANCELADO no nome — o arquivo continua na MESMA
// pasta (ANO/MES/TIPO), só o nome muda. Retorna o novo caminho.
func MarcarCancelado(caminhoAtual string, d document.Document) (string, error) {
	d.Status = "CANCELADO"
	novoNome := FileName(d) + filepath.Ext(caminhoAtual)
	novoPath := filepath.Join(filepath.Dir(caminhoAtual), novoNome)

	if err := os.Rename(caminhoAtual, novoPath); err != nil {
		return "", fmt.Errorf("renomear %s -> %s: %w", caminhoAtual, novoPath, err)
	}

	return novoPath, nil
}

// PlaceEventoSemReferencia é o caminho de segurança: quando um evento de
// cancelamento chega e a nota original NÃO foi encontrada nesta mesma
// rodada (pode ter sido processada numa execução anterior), grava o evento
// mesmo assim — na pasta do mês do EVENTO, tipo TipoNFES, pra não perder o
// registro. Fica visível junto das notas normais, mas com dado incompleto
// (não temos fornecedor/valor no evento em si).
func PlaceEventoSemReferencia(baseDir string, e document.EventoCancelamento, content []byte) (string, error) {
	doc := document.Document{
		Tipo:       document.TipoNFES,
		Fornecedor: "NOTA NAO ENCONTRADA NESTA RODADA",
		Data:       e.Data,
		Numero:     e.ChaveOriginal,
		Status:     "CANCELADO-SEM-REFERENCIA",
	}
	return PlaceDocument(baseDir, doc, ".xml", content)
}

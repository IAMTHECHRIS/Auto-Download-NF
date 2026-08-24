//go:build !windows

// Stub pra sistemas que não são Windows — a janela gráfica (WebView2) só
// existe lá. Em outros sistemas, o cmd/coletor cai pro assistente de texto
// do appconfig.Setup() (ver runtime.GOOS check em cmd/coletor/main.go).
package instalador

func Executar() bool {
	return false
}

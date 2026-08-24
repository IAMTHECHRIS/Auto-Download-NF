//go:build windows

// Package painel mostra o catálogo de notas já baixadas (internal/catalogo)
// numa janela nativa, com um botão pra buscar notas novas na hora e outro
// pra reconfigurar. Só abre quando alguém dá duplo-clique no .exe
// manualmente — a tarefa agendada diária roda sem GUI nenhuma (ver
// cmd/coletor/main.go).
package painel

import (
	_ "embed"
	"encoding/json"
	"os/exec"
	"runtime"

	"sieg-automation/internal/appconfig"
	"sieg-automation/internal/catalogo"
	"sieg-automation/internal/coletanfe"
	"sieg-automation/internal/coletansfe"

	"github.com/webview/webview_go"
)

//go:embed painel.html
var painelHTML string

func init() {
	// mesmo motivo do pacote instalador: janela nativa precisa ficar presa
	// numa thread de SO fixa.
	runtime.LockOSThread()
}

// Abrir mostra o painel e bloqueia até o usuário fechar a janela. Devolve
// true se o usuário pediu pra reconfigurar (apagar a configuração atual e
// refazer o assistente inicial) — quem chama decide o que fazer com isso.
func Abrir(cfg appconfig.Config) (bool, error) {
	reconfigurar := false

	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Coletor de Notas Fiscais — Painel")
	w.SetSize(860, 620, webview.HintNone)

	w.Bind("listarCatalogo", func() string {
		entradas, err := catalogo.Listar(cfg.PastaSaida)
		if err != nil {
			b, _ := json.Marshal(map[string]any{"ok": false, "erro": err.Error()})
			return string(b)
		}
		b, _ := json.Marshal(map[string]any{"ok": true, "entradas": entradas})
		return string(b)
	})

	w.Bind("buscarAgora", func() string {
		var erros []string
		if err := coletanfe.Run(cfg); err != nil {
			erros = append(erros, "NFe: "+err.Error())
		}
		if err := coletansfe.Run(cfg); err != nil {
			erros = append(erros, "NFSe: "+err.Error())
		}
		if len(erros) > 0 {
			b, _ := json.Marshal(map[string]any{"ok": false, "erros": erros})
			return string(b)
		}
		return `{"ok":true}`
	})

	w.Bind("abrirPasta", func(caminho string) {
		// /select, marca o arquivo dentro do Explorer, já mostrando a
		// pasta certa — sem precisar navegar manualmente.
		exec.Command("explorer", "/select,"+caminho).Start()
	})

	w.Bind("reconfigurar", func() {
		reconfigurar = true
		w.Terminate()
	})

	w.SetHtml(painelHTML)
	w.Run()

	return reconfigurar, nil
}

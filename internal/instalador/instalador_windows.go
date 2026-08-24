//go:build windows

// Package instalador é a janela de configuração inicial — visual, nativa
// (usa o WebView2 que já vem no Windows, sem abrir navegador de verdade),
// em vez do assistente de texto no terminal. Mesmo resultado final: grava
// um config.json que o appconfig.Load() já sabe ler.
package instalador

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"runtime"

	"sieg-automation/internal/appconfig"
	"sieg-automation/internal/certload"

	"github.com/sqweek/dialog"
	"github.com/webview/webview_go"
)

//go:embed interface.html
var interfaceHTML string

func init() {
	// OBRIGATÓRIO no Windows: diálogos nativos (abrir arquivo/pasta) e a
	// janela do webview são amarrados à thread do SO que os criou. O Go
	// pode migrar goroutines entre threads livremente por padrão — isso
	// quebra silenciosamente qualquer chamada Win32 que dependa de "qual
	// thread criou o quê". LockOSThread trava a goroutine atual numa
	// thread fixa antes de qualquer coisa gráfica ser criada.
	runtime.LockOSThread()
}

// Executar abre a janela de configuração e bloqueia até o usuário terminar
// (ou fechar a janela). Devolve true se salvou uma configuração válida.
func Executar() bool {
	salvou := false

	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Coletor de Notas Fiscais — Configuração")
	w.SetSize(560, 720, webview.HintFixed)

	// IMPORTANTE: diálogos nativos do Windows (abrir arquivo/pasta) precisam
	// rodar na MESMA thread que criou a janela — o Bind() do webview roda o
	// callback numa goroutine separada, então chamar o diálogo direto ali
	// trava/falha silenciosamente. w.Dispatch() agenda a chamada pra rodar
	// na thread certa; o canal serve só pra esperar o resultado voltar.
	w.Bind("escolherArquivo", func() string {
		resultado := make(chan string, 1)
		w.Dispatch(func() {
			caminho, err := dialog.File().
				Filter("Certificado digital", "pfx").
				Title("Selecione o certificado .pfx").
				Load()
			if err != nil {
				resultado <- ""
				return
			}
			resultado <- caminho
		})
		return <-resultado
	})

	w.Bind("escolherPasta", func() string {
		resultado := make(chan string, 1)
		w.Dispatch(func() {
			caminho, err := dialog.Directory().
				Title("Selecione a pasta de saída (fora de sync de nuvem)").
				Browse()
			if err != nil {
				resultado <- ""
				return
			}
			resultado <- caminho
		})
		return <-resultado
	})

	w.Bind("salvarConfiguracao", func(cfgJSON string) string {
		var cfg appconfig.Config
		if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
			return respostaErro(fmt.Sprintf("dados inválidos: %v", err))
		}

		// valida o certificado ANTES de salvar — se a senha estiver errada
		// ou o arquivo for inválido, o usuário fica sabendo na hora, não
		// só quando o coletor rodar sozinho de madrugada.
		if _, err := certload.FromPFXValidado(cfg.CertificadoPfx, cfg.CertificadoSenha); err != nil {
			return respostaErro(fmt.Sprintf("certificado: %v", err))
		}

		if err := appconfig.Save(cfg); err != nil {
			return respostaErro(fmt.Sprintf("salvar config: %v", err))
		}

		salvou = true
		return respostaOK("Certificado validado e configuração salva!")
	})

	w.Bind("fecharJanela", func() {
		w.Terminate()
	})

	w.SetHtml(interfaceHTML)
	w.Run()

	return salvou
}

func respostaOK(msg string) string {
	b, _ := json.Marshal(map[string]any{"ok": true, "mensagem": msg})
	return string(b)
}

func respostaErro(msg string) string {
	b, _ := json.Marshal(map[string]any{"ok": false, "erro": msg})
	return string(b)
}

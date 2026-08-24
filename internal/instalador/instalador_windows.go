//go:build windows

// Package instalador é a janela de configuração inicial — visual, nativa
// (usa o WebView2 que já vem no Windows, sem abrir navegador de verdade),
// em vez do assistente de texto no terminal. Mesmo resultado final: grava
// um config.json que o appconfig.Load() já sabe ler.
package instalador

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"sieg-automation/internal/appconfig"
	"sieg-automation/internal/certload"

	"github.com/webview/webview_go"
)

//go:embed interface.html
var interfaceHTML string

func init() {
	// OBRIGATÓRIO no Windows: a janela do webview é amarrada à thread do SO
	// que a criou. LockOSThread trava a goroutine atual numa thread fixa
	// antes de qualquer coisa gráfica ser criada.
	runtime.LockOSThread()
}

// escolherViaPowerShell abre um diálogo nativo do Windows rodando um
// script PowerShell EM OUTRO PROCESSO (não outra goroutine, outro
// processo mesmo). Isso não é só preferência — é o que resolve o bug
// anterior: o WebView2 usa COM internamente pra se desenhar, e a
// biblioteca de diálogo que eu usava antes (sqweek/dialog) também
// inicializa COM, na mesma thread. Quando as duas tentam "reivindicar"
// o COM de jeitos diferentes, o Windows recusa em silêncio — sem erro,
// sem crash, só não abre nada. Rodando num processo separado, o COM do
// diálogo nunca encosta no COM do WebView2: são processos diferentes,
// cada um com sua própria memória.
func escolherViaPowerShell(script string) (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-STA", "-WindowStyle", "Hidden", "-Command", script)
	// HideWindow por si só ainda cria a janela do console (só oculta depois),
	// o que gera aquele flash/delay. CREATE_NO_WINDOW (0x08000000) diz pro
	// Windows nem criar a janela — processo nasce sem console nenhum.
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	var saida, erros bytes.Buffer
	cmd.Stdout = &saida
	cmd.Stderr = &erros
	err := cmd.Run()

	// Diagnóstico: grava SEMPRE que o PowerShell escrever algo em stderr ou
	// falhar, num arquivo ao lado do .exe. Já tentei três vezes "no escuro"
	// sem máquina Windows pra testar — dessa vez, se continuar falhando, o
	// arquivo abaixo tem a mensagem de erro .NET real, não mais um chute.
	if err != nil || erros.Len() > 0 {
		registrarDiagnostico(script, saida.String(), erros.String(), err)
	}

	if err != nil {
		return "", err
	}
	return strings.TrimSpace(saida.String()), nil
}

func registrarDiagnostico(script, stdout, stderr string, err error) {
	exePath, _ := os.Executable()
	logPath := filepath.Join(filepath.Dir(exePath), "diagnostico-dialogo.log")

	linhaErr := "nil"
	if err != nil {
		linhaErr = err.Error()
	}

	conteudo := fmt.Sprintf(
		"=== %s ===\nerro Go: %s\nstdout: %q\nstderr: %q\nscript:\n%s\n\n",
		time.Now().Format(time.RFC3339), linhaErr, stdout, stderr, script,
	)

	f, abrirErr := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if abrirErr != nil {
		return
	}
	defer f.Close()
	f.WriteString(conteudo)
}

// Executar abre a janela de configuração e bloqueia até o usuário terminar
// (ou fechar a janela). Devolve true se salvou uma configuração válida.
func Executar() bool {
	salvou := false

	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Coletor de Notas Fiscais — Configuração")
	w.SetSize(560, 720, webview.HintFixed)

	w.Bind("escolherArquivo", func() string {
		caminho, err := escolherViaPowerShell(`
Add-Type -AssemblyName System.Windows.Forms
$owner = New-Object System.Windows.Forms.Form
$owner.TopMost = $true
$owner.ShowInTaskbar = $false
$owner.StartPosition = 'CenterScreen'
$owner.Size = New-Object System.Drawing.Size(0,0)
$owner.Show()
$owner.Activate()
$f = New-Object System.Windows.Forms.OpenFileDialog
$f.Title = "Selecione o certificado .pfx"
$f.Filter = "Certificado digital (*.pfx)|*.pfx|Todos os arquivos (*.*)|*.*"
if ($f.ShowDialog($owner) -eq [System.Windows.Forms.DialogResult]::OK) {
    Write-Output $f.FileName
}
$owner.Dispose()
`)
		if err != nil {
			return ""
		}
		return caminho
	})

	w.Bind("escolherPasta", func() string {
		// Shell.Application é COM puro do Explorer (diferente das tentativas
		// anteriores em WinForms) — mas SEM um HWND-dono, o diálogo nasce
		// atrás da janela principal do webview (mesmo sintoma silencioso do
		// início: "não abre" quando na verdade abriu, só que escondido).
		// A correção que já funcionou pro certificado foi dar um dono
		// TopMost pro diálogo; aqui reaplico a mesma ideia, passando o
		// Handle real da Form-dona pro BrowseForFolder.
		caminho, err := escolherViaPowerShell(`
Add-Type -AssemblyName System.Windows.Forms
$owner = New-Object System.Windows.Forms.Form
$owner.TopMost = $true
$owner.ShowInTaskbar = $false
$owner.StartPosition = 'CenterScreen'
$owner.Size = New-Object System.Drawing.Size(0,0)
$owner.Show()
$owner.Activate()
$shell = New-Object -ComObject Shell.Application
$pasta = $shell.BrowseForFolder($owner.Handle.ToInt32(), "Selecione a pasta de saída (fora de sync de nuvem)", 0, 0)
$owner.Dispose()
if ($pasta -ne $null) {
    Write-Output $pasta.Self.Path
}
`)
		if err != nil {
			return ""
		}
		return caminho
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

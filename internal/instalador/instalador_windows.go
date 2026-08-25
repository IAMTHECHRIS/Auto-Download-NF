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
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"io-nf-automation/internal/appconfig"
	"io-nf-automation/internal/certload"

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

// debugLog registra CADA passo da instalação (clique em botão, script
// PowerShell rodado, saída, erro, tempo) num único arquivo ao lado do
// .exe. Existe só pra diagnóstico — depois de várias tentativas de
// correção sem uma máquina Windows disponível pra testar, é mais rápido
// pedir esse arquivo pro usuário do que continuar chutando.
var debugLog = log.New(io.Discard, "", 0)

func iniciarDebugLog() *os.File {
	exePath, err := os.Executable()
	if err != nil {
		exePath = "."
	}
	logPath := filepath.Join(filepath.Dir(exePath), "instalador-debug.log")

	// O_TRUNC: cada execução começa um log novo e limpo — mais fácil de ler
	// e de mandar pra mim do que um arquivo que cresce pra sempre.
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil
	}
	debugLog = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
	debugLog.Printf("=== instalador iniciado === GOOS=%s GOARCH=%s", runtime.GOOS, runtime.GOARCH)

	if info, err := escolherViaPowerShell(`
Write-Output ("Windows: " + [Environment]::OSVersion.VersionString)
Write-Output ("PowerShell: " + $PSVersionTable.PSVersion.ToString())
Write-Output ("64-bit OS: " + [Environment]::Is64BitOperatingSystem)
Write-Output ("Usuario: " + [Environment]::UserName)
`); err == nil {
		debugLog.Printf("info do sistema:\n%s", info)
	} else {
		debugLog.Printf("falha ao coletar info do sistema: %v", err)
	}

	return f
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
	inicio := time.Now()
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
	duracao := time.Since(inicio)

	linhaErr := "nil"
	if err != nil {
		linhaErr = err.Error()
	}
	debugLog.Printf(
		"powershell rodou em %s | erro Go: %s | stdout: %q | stderr: %q | script: %s",
		duracao, linhaErr, saida.String(), erros.String(), strings.TrimSpace(script),
	)

	if err != nil {
		return "", err
	}
	return strings.TrimSpace(saida.String()), nil
}

// Executar abre a janela de configuração e bloqueia até o usuário terminar
// (ou fechar a janela). Devolve true se salvou uma configuração válida.
func Executar() bool {
	if f := iniciarDebugLog(); f != nil {
		defer f.Close()
	}
	defer func() {
		if r := recover(); r != nil {
			debugLog.Printf("PANIC recuperado: %v", r)
			panic(r)
		}
	}()

	salvou := false

	debugLog.Printf("criando janela webview...")
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Coletor de Notas Fiscais — Configuração")
	w.SetSize(560, 720, webview.HintFixed)
	debugLog.Printf("janela criada")

	w.Bind("escolherArquivo", func() string {
		debugLog.Printf(">> clique: escolherArquivo (certificado)")
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
		debugLog.Printf("<< resultado escolherArquivo: caminho=%q err=%v", caminho, err)
		if err != nil {
			return ""
		}
		return caminho
	})

	w.Bind("escolherPasta", func() string {
		debugLog.Printf(">> clique: escolherPasta")
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
		debugLog.Printf("<< resultado escolherPasta: caminho=%q err=%v", caminho, err)
		if err != nil {
			return ""
		}
		return caminho
	})

	w.Bind("salvarConfiguracao", func(cfgJSON string) string {
		debugLog.Printf(">> clique: salvarConfiguracao | payload=%s", mascarSenha(cfgJSON))
		var cfg appconfig.Config
		if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
			debugLog.Printf("<< erro: json inválido: %v", err)
			return respostaErro(fmt.Sprintf("dados inválidos: %v", err))
		}

		// valida o certificado ANTES de salvar — se a senha estiver errada
		// ou o arquivo for inválido, o usuário fica sabendo na hora, não
		// só quando o coletor rodar sozinho de madrugada.
		if _, err := certload.FromPFXValidado(cfg.CertificadoPfx, cfg.CertificadoSenha); err != nil {
			debugLog.Printf("<< erro: certificado inválido: %v", err)
			return respostaErro(fmt.Sprintf("certificado: %v", err))
		}

		if err := appconfig.Save(cfg); err != nil {
			debugLog.Printf("<< erro: salvar config: %v", err)
			return respostaErro(fmt.Sprintf("salvar config: %v", err))
		}

		salvou = true
		debugLog.Printf("<< configuração salva com sucesso")
		return respostaOK("Certificado validado e configuração salva!")
	})

	w.Bind("fecharJanela", func() {
		debugLog.Printf(">> fecharJanela chamado")
		w.Terminate()
	})

	w.SetHtml(interfaceHTML)
	debugLog.Printf("HTML carregado, iniciando loop principal (w.Run)...")
	w.Run()
	debugLog.Printf("w.Run() retornou, encerrando. salvou=%v", salvou)

	return salvou
}

func mascarSenha(cfgJSON string) string {
	var m map[string]any
	if json.Unmarshal([]byte(cfgJSON), &m) != nil {
		return "(não foi possível decodificar pra log)"
	}
	if _, ok := m["certificado_senha"]; ok {
		m["certificado_senha"] = "***"
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func respostaOK(msg string) string {
	b, _ := json.Marshal(map[string]any{"ok": true, "mensagem": msg})
	return string(b)
}

func respostaErro(msg string) string {
	b, _ := json.Marshal(map[string]any{"ok": false, "erro": msg})
	return string(b)
}

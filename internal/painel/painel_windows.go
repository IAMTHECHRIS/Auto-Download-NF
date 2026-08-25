//go:build windows

// Package painel mostra o catálogo de notas já baixadas (internal/catalogo)
// numa janela nativa, com abas pra buscar notas novas, buscar uma nota
// específica por chave, verificar se tudo já foi copiado pra pasta de
// destino, e editar a configuração (CNPJ, estado, certificado, pastas) sem
// precisar refazer o assistente inicial do zero. Só abre quando alguém dá
// duplo-clique no .exe manualmente — a tarefa agendada roda sem GUI
// nenhuma (ver cmd/coletor/main.go).
package painel

import (
	"archive/zip"
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

	"sieg-automation/internal/appconfig"
	"sieg-automation/internal/catalogo"
	"sieg-automation/internal/certload"
	"sieg-automation/internal/coletanfe"
	"sieg-automation/internal/coletansfe"
	"sieg-automation/internal/verificacao"
	"sieg-automation/internal/wintask"

	"github.com/webview/webview_go"
)

//go:embed painel.html
var painelHTML string

func init() {
	// mesmo motivo do pacote instalador: janela nativa precisa ficar presa
	// numa thread de SO fixa.
	runtime.LockOSThread()
}

// debugLog: mesmo esquema do instalador — registra cada ação num arquivo
// ao lado do .exe, recriado do zero a cada abertura do painel. As chamadas
// novas aqui (buscarPorChave, sobretudo) usam um tipo de consulta na SEFAZ
// que eu nunca testei contra o webservice real — importante ter rastro.
var debugLog = log.New(io.Discard, "", 0)

func iniciarDebugLog() *os.File {
	exePath, err := os.Executable()
	if err != nil {
		exePath = "."
	}
	f, err := os.OpenFile(filepath.Join(filepath.Dir(exePath), "painel-debug.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil
	}
	debugLog = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
	debugLog.Printf("=== painel iniciado ===")
	return f
}

const espacamentoMinimoChave = 3 * time.Second

// Abrir mostra o painel e bloqueia até o usuário fechar a janela. Devolve
// true se o usuário pediu pra reconfigurar (apagar a configuração atual e
// refazer o assistente inicial) — quem chama decide o que fazer com isso.
func Abrir(cfg appconfig.Config) (bool, error) {
	if f := iniciarDebugLog(); f != nil {
		defer f.Close()
	}

	reconfigurar := false
	var ultimaConsultaChave time.Time
	var contadorConsultasChave int

	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Coletor de Notas Fiscais — Painel")
	w.SetSize(900, 640, webview.HintNone)

	w.Bind("listarCatalogo", func() string {
		entradas, err := catalogo.Listar(cfg.PastaSaida)
		if err != nil {
			debugLog.Printf("listarCatalogo erro: %v", err)
			b, _ := json.Marshal(map[string]any{"ok": false, "erro": err.Error()})
			return string(b)
		}
		debugLog.Printf("listarCatalogo: %d entradas", len(entradas))
		b, _ := json.Marshal(map[string]any{"ok": true, "entradas": entradas})
		return string(b)
	})

	w.Bind("buscarAgora", func() string {
		debugLog.Printf(">> buscarAgora")
		var erros []string
		if err := coletanfe.Run(cfg); err != nil {
			erros = append(erros, "NFe: "+err.Error())
		}
		if err := coletansfe.Run(cfg); err != nil {
			erros = append(erros, "NFSe: "+err.Error())
		}
		debugLog.Printf("<< buscarAgora erros=%v", erros)
		if len(erros) > 0 {
			b, _ := json.Marshal(map[string]any{"ok": false, "erros": erros})
			return string(b)
		}
		return `{"ok":true}`
	})

	w.Bind("buscarPorChave", func(chave string) string {
		chave = strings.TrimSpace(chave)
		debugLog.Printf(">> buscarPorChave chave=%s", chave)
		if len(chave) != 44 {
			return respostaErro("A chave de acesso precisa ter 44 dígitos.")
		}
		for _, r := range chave {
			if r < '0' || r > '9' {
				return respostaErro("A chave de acesso só tem números.")
			}
		}

		// espaçamento mínimo entre consultas avulsas — não sabemos a cota
		// exata que a SEFAZ tolera pra esse tipo de consulta pontual
		// (consChNFe), então evita disparar em sequência rápida.
		if !ultimaConsultaChave.IsZero() {
			espera := espacamentoMinimoChave - time.Since(ultimaConsultaChave)
			if espera > 0 {
				time.Sleep(espera)
			}
		}
		ultimaConsultaChave = time.Now()
		contadorConsultasChave++

		doc, caminho, err := coletanfe.BuscarUma(cfg, chave)
		debugLog.Printf("<< buscarPorChave doc=%+v caminho=%s err=%v", doc, caminho, err)
		if err != nil {
			return respostaErro(err.Error())
		}

		msg := fmt.Sprintf("Nota %s de %s salva em: %s", doc.Numero, doc.Fornecedor, caminho)
		if contadorConsultasChave >= 5 {
			msg += fmt.Sprintf(" — atenção: já são %d consultas avulsas nessa sessão; não temos confirmação do limite diário da SEFAZ pra esse tipo de busca, evite repetir sem necessidade.", contadorConsultasChave)
		}
		return respostaOK(msg)
	})

	// pastaDestino agora é escolhida NA HORA pelo usuário (botão "Procurar"
	// na própria aba Verificar cópia), não fica salva no config.json — o
	// usuário pode querer checar contra pastas diferentes em momentos
	// diferentes, não só uma fixa.
	w.Bind("verificarCopia", func(pastaDestino string) string {
		debugLog.Printf(">> verificarCopia pastaDestino=%s", pastaDestino)
		faltando, err := verificacao.Verificar(cfg.PastaSaida, pastaDestino)
		debugLog.Printf("<< verificarCopia faltando=%d err=%v", len(faltando), err)
		if err != nil {
			b, _ := json.Marshal(map[string]any{"ok": false, "erro": err.Error()})
			return string(b)
		}
		b, _ := json.Marshal(map[string]any{"ok": true, "faltando": faltando})
		return string(b)
	})

	// gerarZip empacota os XMLs selecionados na aba "Verificar cópia" pra o
	// usuário levar pra onde precisar. Salva DENTRO da própria pasta de
	// destino que ele já escolheu pra verificar — contextualmente é o
	// lugar óbvio: é exatamente pra lá que essas notas iam de qualquer
	// jeito, o ZIP só facilita levar tudo de uma vez.
	w.Bind("gerarZip", func(caminhosJSON string, pastaDestino string) string {
		var caminhos []string
		if err := json.Unmarshal([]byte(caminhosJSON), &caminhos); err != nil {
			return respostaErro("dados inválidos: " + err.Error())
		}
		if len(caminhos) == 0 {
			return respostaErro("selecione ao menos uma nota.")
		}
		if strings.TrimSpace(pastaDestino) == "" {
			return respostaErro("escolha a pasta de destino antes.")
		}

		nomeZip := fmt.Sprintf("notas-selecionadas-%s.zip", time.Now().Format("20060102-1504"))
		caminhoZip := filepath.Join(pastaDestino, nomeZip)
		debugLog.Printf(">> gerarZip destino=%s itens=%d", caminhoZip, len(caminhos))

		if err := criarZip(caminhoZip, caminhos); err != nil {
			debugLog.Printf("<< gerarZip erro: %v", err)
			return respostaErro(err.Error())
		}
		debugLog.Printf("<< gerarZip ok")
		return respostaOK(fmt.Sprintf("ZIP com %d nota(s) criado em: %s", len(caminhos), caminhoZip))
	})

	w.Bind("obterBuscaAutomatica", func() bool {
		return cfg.AutoBuscarAoAbrir
	})

	w.Bind("definirBuscaAutomatica", func(ligado bool) {
		cfg.AutoBuscarAoAbrir = ligado
		if err := appconfig.Save(cfg); err != nil {
			debugLog.Printf("erro ao salvar busca automática: %v", err)
		}
		debugLog.Printf("busca automática ao abrir: %v", ligado)
	})

	w.Bind("carregarConfiguracao", func() string {
		b, _ := json.Marshal(map[string]any{
			"cnpj":            cfg.CNPJ,
			"cUFAutor":        cfg.CUFAutor,
			"certificado_pfx": cfg.CertificadoPfx,
			"pasta_saida":     cfg.PastaSaida,
		})
		return string(b)
	})

	w.Bind("salvarConfiguracaoPainel", func(cfgJSON string) string {
		var entrada struct {
			CNPJ             string `json:"cnpj"`
			CUFAutor         string `json:"cUFAutor"`
			CertificadoPfx   string `json:"certificado_pfx"`
			CertificadoSenha string `json:"certificado_senha"`
			PastaSaida       string `json:"pasta_saida"`
		}
		if err := json.Unmarshal([]byte(cfgJSON), &entrada); err != nil {
			return respostaErro("dados inválidos: " + err.Error())
		}

		novoCfg := cfg
		novoCfg.CNPJ = entrada.CNPJ
		novoCfg.CUFAutor = entrada.CUFAutor
		novoCfg.CertificadoPfx = entrada.CertificadoPfx
		novoCfg.PastaSaida = entrada.PastaSaida
		if strings.TrimSpace(entrada.CertificadoSenha) != "" {
			novoCfg.CertificadoSenha = entrada.CertificadoSenha
		}

		if _, err := certload.FromPFXValidado(novoCfg.CertificadoPfx, novoCfg.CertificadoSenha); err != nil {
			return respostaErro("certificado: " + err.Error())
		}
		if err := appconfig.Save(novoCfg); err != nil {
			return respostaErro("salvar configuração: " + err.Error())
		}

		cfg = novoCfg
		debugLog.Printf("configuração atualizada pelo painel: cnpj=%s pasta_saida=%s", cfg.CNPJ, cfg.PastaSaida)

		aviso := ""
		if strings.TrimSpace(entrada.PastaSaida) != "" {
			// mudar a pasta de saída aqui NÃO move o programa nem os XMLs já
			// baixados — só passa a valer pras próximas coletas. Deixa isso
			// explícito pra não criar expectativa errada.
			aviso = " (arquivos já baixados continuam onde estavam — isso só vale pra notas novas)"
		}
		return respostaOK("Configuração salva." + aviso)
	})

	w.Bind("abrirPasta", func(caminho string) {
		// /select, marca o arquivo dentro do Explorer, já mostrando a
		// pasta certa — sem precisar navegar manualmente.
		exec.Command("explorer", "/select,"+caminho).Start()
	})

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
		debugLog.Printf("escolherArquivo: caminho=%q err=%v", caminho, err)
		if err != nil {
			return ""
		}
		return caminho
	})

	w.Bind("escolherPasta", func() string {
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
$pasta = $shell.BrowseForFolder($owner.Handle.ToInt32(), "Selecione a pasta", 0, 0)
$owner.Dispose()
if ($pasta -ne $null) {
    Write-Output $pasta.Self.Path
}
`)
		debugLog.Printf("escolherPasta: caminho=%q err=%v", caminho, err)
		if err != nil {
			return ""
		}
		return caminho
	})

	w.Bind("reconfigurar", func() {
		debugLog.Printf(">> reconfigurar chamado")
		reconfigurar = true
		w.Terminate()
	})

	w.Bind("desinstalarTarefaAgendada", func() string {
		debugLog.Printf(">> desinstalarTarefaAgendada chamado")
		if err := wintask.RemoverTarefa(); err != nil {
			debugLog.Printf("<< erro ao remover tarefa: %v", err)
			return respostaErro(err.Error())
		}
		debugLog.Printf("<< tarefa agendada removida")
		return respostaOK("Tarefa agendada removida. O programa e as notas continuam onde estavam — só a coleta automática diária foi desligada.")
	})

	w.Bind("fecharJanela", func() {
		w.Terminate()
	})

	w.SetHtml(painelHTML)
	w.Run()

	return reconfigurar, nil
}

// escolherViaPowerShell — mesma implementação do internal/instalador
// (duplicada de propósito: são pacotes independentes, e é um script curto;
// não vale a complexidade de extrair um terceiro pacote compartilhado só
// pra isso).
func escolherViaPowerShell(script string) (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-STA", "-WindowStyle", "Hidden", "-Command", script)
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	var saida, erros bytes.Buffer
	cmd.Stdout = &saida
	cmd.Stderr = &erros
	err := cmd.Run()
	debugLog.Printf("powershell | erro=%v | stdout=%q | stderr=%q", err, saida.String(), erros.String())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(saida.String()), nil
}

// criarZip empacota cada arquivo em "arquivos" no zip criado em "destino",
// usando só o nome-base de cada um (sem estrutura de pasta ANO/MES/TIPO
// dentro do zip — quem recebe só quer os XMLs soltos, prontos pra usar).
func criarZip(destino string, arquivos []string) error {
	f, err := os.Create(destino)
	if err != nil {
		return fmt.Errorf("criar arquivo zip: %w", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	for _, caminho := range arquivos {
		if err := adicionarAoZip(zw, caminho); err != nil {
			return fmt.Errorf("adicionar %s ao zip: %w", filepath.Base(caminho), err)
		}
	}
	return nil
}

func adicionarAoZip(zw *zip.Writer, caminho string) error {
	src, err := os.Open(caminho)
	if err != nil {
		return err
	}
	defer src.Close()

	w, err := zw.Create(filepath.Base(caminho))
	if err != nil {
		return err
	}
	_, err = io.Copy(w, src)
	return err
}

func respostaOK(msg string) string {
	b, _ := json.Marshal(map[string]any{"ok": true, "mensagem": msg})
	return string(b)
}

func respostaErro(msg string) string {
	b, _ := json.Marshal(map[string]any{"ok": false, "erro": msg})
	return string(b)
}

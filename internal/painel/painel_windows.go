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

	"io-nf-automation/internal/appconfig"
	"io-nf-automation/internal/catalogo"
	"io-nf-automation/internal/certload"
	"io-nf-automation/internal/coletanfe"
	"io-nf-automation/internal/coletansfe"
	"io-nf-automation/internal/verificacao"
	"io-nf-automation/internal/wintask"

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

	// resolverAsync entrega o resultado de uma chamada assíncrona pro JS. As
	// funções ligadas que fazem rede/disco pesado (buscarAgora, buscarPorChave,
	// verificarCopia, baixarSelecionados, gerarZip) NÃO retornam valor — elas
	// disparam o trabalho numa goroutine e chamam isso aqui quando terminam.
	// w.Dispatch() é a única forma seThreadsafe de mexer na UI a partir de uma
	// goroutine nesse binding do webview — ele enfileira a função pra rodar na
	// thread certa sem bloquear o loop de mensagens do Windows enquanto isso.
	// Ver window.__resolverAsync / chamarAsync em painel.html pro lado JS.
	resolverAsync := func(id int, resultadoJSON string) {
		w.Dispatch(func() {
			codificado, _ := json.Marshal(resultadoJSON)
			w.Eval(fmt.Sprintf("window.__resolverAsync(%d, %s)", id, codificado))
		})
	}

	w.Bind("listarCatalogo", func() string {
		entradas, err := catalogo.Listar(cfg.PastaEfetiva())
		if err != nil {
			debugLog.Printf("listarCatalogo erro: %v", err)
			b, _ := json.Marshal(map[string]any{"ok": false, "erro": err.Error()})
			return string(b)
		}
		debugLog.Printf("listarCatalogo: %d entradas", len(entradas))
		b, _ := json.Marshal(map[string]any{"ok": true, "entradas": entradas})
		return string(b)
	})

	w.Bind("buscarAgora", func(id int) {
		go func() {
			debugLog.Printf(">> buscarAgora")
			var erros []string

			resumoNFe, err := coletanfe.Run(cfg)
			if err != nil {
				erros = append(erros, "NFe: "+err.Error())
			}
			resumoNFSe, err := coletansfe.Run(cfg)
			if err != nil {
				erros = append(erros, "NFSe: "+err.Error())
			}
			debugLog.Printf("<< buscarAgora nfe=%+v nfse=%+v erros=%v", resumoNFe, resumoNFSe, erros)

			// diagnóstico: "0 notas novas" pode significar duas coisas bem
			// diferentes — já está em dia (bom sinal) ou parou no meio do
			// caminho antes de achar algo novo (MaxPaginas acabou). Sem
			// distinguir isso o usuário fica sem saber se precisa só clicar
			// "Buscar" de novo pra continuar de onde parou.
			var diagnostico string
			switch {
			case resumoNFe.CStat != "" && resumoNFe.CStat != "137" && resumoNFe.CStat != "138":
				// rejeitado de verdade e não deu pra auto-corrigir — motivo
				// real da SEFAZ, não escondido atrás de "sem notas novas".
				diagnostico = fmt.Sprintf("⚠ NFEC/CT-e: a SEFAZ RECUSOU o pedido (cStat=%s: %s) — não é \"sem notas novas\", é rejeição de verdade. NÃO fique clicando \"Buscar\" repetidamente — se for bloqueio por reinício de NSU, pode levar até 1h pra liberar sozinho.", resumoNFe.CStat, resumoNFe.XMotivo)
			case resumoNFe.AutoCorrigido:
				diagnostico = fmt.Sprintf("NFEC/CT-e: o checkpoint local estava desatualizado — a SEFAZ recusou (cStat=%s: %s), mas revelou o NSU certo (%d) e o programa já se auto-corrigiu e retomou dali.", resumoNFe.CStat, resumoNFe.XMotivo, resumoNFe.NSUAutoCorrigido)
				if resumoNFe.Novas == 0 && !resumoNFe.EmDia() {
					diagnostico += " Ainda tem mais pra varrer — clique em \"Buscar notas agora\" de novo."
				}
			case resumoNFe.Novas == 0 && resumoNFe.EmDia():
				diagnostico = "NFEC/CT-e: sem notas novas — já está em dia com a SEFAZ."
			case resumoNFe.Novas == 0 && resumoNFe.Paginas > 0:
				diagnostico = fmt.Sprintf("NFEC/CT-e: sem notas novas nesta rodada, mas ainda tem mais pra varrer (parou no NSU %s de %s após %d página(s)) — clique em \"Buscar notas agora\" de novo pra continuar.", resumoNFe.UltNSU, resumoNFe.MaxNSU, resumoNFe.Paginas)
			}
			if resumoNFSe.ParouPorLimitePaginas {
				if diagnostico != "" {
					diagnostico += " "
				}
				diagnostico += fmt.Sprintf("NFS-e: rodada terminou no limite de %d página(s) sem esvaziar — pode ter mais, busque de novo.", coletansfe.MaxPaginas)
			} else if diagNFSe := diagnosticoNFSe(resumoNFSe); diagNFSe != "" {
				if diagnostico != "" {
					diagnostico += " "
				}
				diagnostico += diagNFSe
			}

			b, _ := json.Marshal(map[string]any{"ok": len(erros) == 0, "erros": erros, "diagnostico": diagnostico})
			resolverAsync(id, string(b))
		}()
	})

	w.Bind("buscarPorChave", func(id int, chave string) {
		go func() {
			chave = strings.TrimSpace(chave)
			debugLog.Printf(">> buscarPorChave chave=%s", chave)
			if len(chave) != 44 {
				resolverAsync(id, respostaErro("A chave de acesso precisa ter 44 dígitos."))
				return
			}
			for _, r := range chave {
				if r < '0' || r > '9' {
					resolverAsync(id, respostaErro("A chave de acesso só tem números."))
					return
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
				resolverAsync(id, respostaErro(err.Error()))
				return
			}

			msg := fmt.Sprintf("Nota %s de %s salva em: %s", doc.Numero, doc.Fornecedor, caminho)
			if contadorConsultasChave >= 5 {
				msg += fmt.Sprintf(" — atenção: já são %d consultas avulsas nessa sessão; não temos confirmação do limite diário da SEFAZ pra esse tipo de busca, evite repetir sem necessidade.", contadorConsultasChave)
			}
			pdfCaminho := strings.TrimSuffix(caminho, filepath.Ext(caminho)) + ".pdf"
			_, errPdf := os.Stat(pdfCaminho)
			b, _ := json.Marshal(map[string]any{"ok": true, "mensagem": msg, "caminho": caminho, "tem_pdf": errPdf == nil})
			resolverAsync(id, string(b))
		}()
	})

	// pastaDestino agora é escolhida NA HORA pelo usuário (botão "Procurar"
	// na própria aba Verificar cópia), não fica salva no config.json — o
	// usuário pode querer checar contra pastas diferentes em momentos
	// diferentes, não só uma fixa.
	w.Bind("verificarCopia", func(id int, pastaDestino string) {
		go func() {
			debugLog.Printf(">> verificarCopia pastaDestino=%s", pastaDestino)
			r, err := verificacao.Verificar(cfg.PastaEfetiva(), pastaDestino)
			debugLog.Printf("<< verificarCopia escopo=%+v totalNoEscopo=%d faltando=%d err=%v", r.Escopo, r.TotalNoEscopo, len(r.Faltando), err)
			if err != nil {
				b, _ := json.Marshal(map[string]any{"ok": false, "erro": err.Error()})
				resolverAsync(id, string(b))
				return
			}
			b, _ := json.Marshal(map[string]any{"ok": true, "faltando": r.Faltando, "escopo": r.Escopo, "total_no_escopo": r.TotalNoEscopo})
			resolverAsync(id, string(b))
		}()
	})

	// gerarZip empacota os XMLs (e, se comPdf, os PDFs correspondentes) das
	// notas selecionadas na aba "Verificar cópia" pra o usuário levar pra
	// onde precisar. Salva DENTRO da própria pasta de destino que ele já
	// escolheu pra verificar. As notas aqui já estão baixadas localmente
	// (vieram do catálogo) — comPdf só junta o .pdf irmão que já existe ao
	// lado do .xml no disco, sem nenhuma consulta nova na SEFAZ.
	w.Bind("gerarZip", func(id int, caminhosJSON string, pastaDestino string, comPdf bool) {
		go func() {
			var caminhos []string
			if err := json.Unmarshal([]byte(caminhosJSON), &caminhos); err != nil {
				resolverAsync(id, respostaErro("dados inválidos: "+err.Error()))
				return
			}
			if len(caminhos) == 0 {
				resolverAsync(id, respostaErro("selecione ao menos uma nota."))
				return
			}
			if strings.TrimSpace(pastaDestino) == "" {
				resolverAsync(id, respostaErro("escolha a pasta de destino antes."))
				return
			}

			arquivos := append([]string{}, caminhos...)
			if comPdf {
				for _, c := range caminhos {
					pdfCaminho := strings.TrimSuffix(c, filepath.Ext(c)) + ".pdf"
					if _, err := os.Stat(pdfCaminho); err == nil {
						arquivos = append(arquivos, pdfCaminho)
					}
				}
			}

			nomeZip := fmt.Sprintf("notas-selecionadas-%s.zip", time.Now().Format("20060102-1504"))
			caminhoZip := filepath.Join(pastaDestino, nomeZip)
			debugLog.Printf(">> gerarZip destino=%s itens=%d comPdf=%v", caminhoZip, len(arquivos), comPdf)

			if err := criarZip(caminhoZip, arquivos); err != nil {
				debugLog.Printf("<< gerarZip erro: %v", err)
				resolverAsync(id, respostaErro(err.Error()))
				return
			}
			debugLog.Printf("<< gerarZip ok")
			resolverAsync(id, respostaOK(fmt.Sprintf("ZIP com %d arquivo(s) criado em: %s", len(arquivos), caminhoZip)))
		}()
	})

	// baixarSelecionados atende tanto a lista Entrada/Saída quanto o
	// resultado de "Buscar por chave": cada item diz de qual nota (pelo
	// caminho do XML, sempre a referência) quer XML e/ou PDF. Um arquivo só
	// -> copia direto; mais de um -> empacota em ZIP (mesmo helper de
	// gerarZip). PDF que não existe (nota sem DANFE gerado ainda, ou NFSe)
	// é ignorado silenciosamente em vez de dar erro.
	w.Bind("baixarSelecionados", func(id int, itensJSON string, pastaDestino string) {
		go func() {
			var itens []struct {
				Caminho string `json:"caminho"`
				XML     bool   `json:"xml"`
				PDF     bool   `json:"pdf"`
			}
			if err := json.Unmarshal([]byte(itensJSON), &itens); err != nil {
				resolverAsync(id, respostaErro("dados inválidos: "+err.Error()))
				return
			}
			if strings.TrimSpace(pastaDestino) == "" {
				resolverAsync(id, respostaErro("escolha a pasta de destino antes."))
				return
			}

			var arquivos []string
			for _, it := range itens {
				if it.Caminho == "" {
					continue
				}
				if it.XML {
					arquivos = append(arquivos, it.Caminho)
				}
				if it.PDF {
					pdfCaminho := strings.TrimSuffix(it.Caminho, filepath.Ext(it.Caminho)) + ".pdf"
					if _, err := os.Stat(pdfCaminho); err == nil {
						arquivos = append(arquivos, pdfCaminho)
					}
				}
			}
			if len(arquivos) == 0 {
				resolverAsync(id, respostaErro("nada pra baixar — marque XML e/ou PDF de pelo menos uma nota (o PDF só existe pra NFe/CT-e já processadas com o DANFE novo)."))
				return
			}
			debugLog.Printf(">> baixarSelecionados destino=%s itens=%d", pastaDestino, len(arquivos))

			if len(arquivos) == 1 {
				destino := filepath.Join(pastaDestino, filepath.Base(arquivos[0]))
				if err := copiarArquivo(arquivos[0], destino); err != nil {
					debugLog.Printf("<< baixarSelecionados erro: %v", err)
					resolverAsync(id, respostaErro(err.Error()))
					return
				}
				debugLog.Printf("<< baixarSelecionados ok (arquivo único)")
				resolverAsync(id, respostaOK("Arquivo salvo em: "+destino))
				return
			}

			nomeZip := fmt.Sprintf("notas-%s.zip", time.Now().Format("20060102-1504"))
			caminhoZip := filepath.Join(pastaDestino, nomeZip)
			if err := criarZip(caminhoZip, arquivos); err != nil {
				debugLog.Printf("<< baixarSelecionados erro: %v", err)
				resolverAsync(id, respostaErro(err.Error()))
				return
			}
			debugLog.Printf("<< baixarSelecionados ok (zip)")
			resolverAsync(id, respostaOK(fmt.Sprintf("ZIP com %d arquivo(s) criado em: %s", len(arquivos), caminhoZip)))
		}()
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
			"ambiente":        cfg.Ambiente,
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
			Ambiente         string `json:"ambiente"`
		}
		if err := json.Unmarshal([]byte(cfgJSON), &entrada); err != nil {
			return respostaErro("dados inválidos: " + err.Error())
		}

		novoCfg := cfg
		novoCfg.CNPJ = entrada.CNPJ
		novoCfg.CUFAutor = entrada.CUFAutor
		novoCfg.CertificadoPfx = entrada.CertificadoPfx
		novoCfg.PastaSaida = entrada.PastaSaida
		novoCfg.Ambiente = entrada.Ambiente
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
		// /select, marca o arquivo dentro do Explorer, já mostrando a pasta
		// certa — sem precisar navegar manualmente. O bug reportado ("abre
		// pasta genérica pra QUALQUER nota, não só as com vírgula no nome")
		// era causado justamente pela tentativa anterior de correção: aspas
		// manuais ao redor do caminho (`/select,"`+caminho+`"`) fazem o
		// próprio exec.Command do Go, ao montar a linha de comando pro
		// Windows, escapar essas aspas literais (viram \" — regra de quoting
		// do MSVCRT que o Go segue), e o explorer.exe recebe um argumento
		// com barras invertidas + aspas soltas no meio do caminho, que ele
		// não reconhece como delimitador de nada. O jeito certo é NÃO
		// colocar aspas manualmente — o exec.Command do Go já aplica a
		// quoting correta sozinho (só entre aspas quando precisa, ex:
		// caminho com espaço/vírgula) exatamente no formato que o
		// CommandLineToArgvW (e por extensão o parser do explorer.exe)
		// espera.
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

	// Desinstalação completa: remove a tarefa agendada e apaga a pasta
	// _Controle inteira (programa, config.json, catálogo, checkpoints,
	// logs) — as notas já baixadas (nfe-compras/nfse, fora do _Controle)
	// NÃO são tocadas. O próprio .exe está DENTRO da pasta que estamos
	// apagando, então não dá pra apagar ele enquanto ainda está rodando
	// (Windows trava arquivo em uso) — por isso agendarAutoLimpeza deixa
	// um processo solto esperando esse aqui fechar pra terminar o serviço.
	w.Bind("desinstalarPrograma", func() string {
		debugLog.Printf(">> desinstalarPrograma chamado")
		if err := wintask.RemoverTarefa(); err != nil {
			debugLog.Printf("<< erro ao remover tarefa: %v", err)
			return respostaErro(err.Error())
		}
		if err := agendarAutoLimpeza(appconfig.PastaControle(cfg.PastaSaida), exePathAtual()); err != nil {
			debugLog.Printf("<< erro ao agendar limpeza: %v", err)
			return respostaErro(err.Error())
		}
		debugLog.Printf("<< desinstalação agendada, fechando em seguida")
		return respostaOK("Desinstalado. Fechando...")
	})

	w.Bind("fecharJanela", func() {
		w.Terminate()
	})

	w.SetHtml(painelHTML)
	w.Run()

	return reconfigurar, nil
}

// exePathAtual devolve o caminho do executável atual, ou "" se não conseguir
// resolver (nesse caso agendarAutoLimpeza só não consegue matar OUTRAS
// instâncias por caminho — o resto da limpeza segue normal).
func exePathAtual() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}

// agendarAutoLimpeza dispara um processo PowerShell DESANEXADO (roda solto,
// não é filho que morre junto com a gente) que espera alguns segundos —
// tempo pro processo atual fechar e soltar o arquivo do .exe — e só então
// apaga a pasta inteira, incluindo o próprio executável.
func agendarAutoLimpeza(pastaControle, exePath string) error {
	// Antes era uma tentativa ÚNICA de Remove-Item com
	// -ErrorAction SilentlyContinue, que ENGOLE qualquer falha sem avisar
	// nada — se o .exe atual não tivesse soltado o arquivo a tempo (comum:
	// antivírus/Defender escaneando o .exe recém-rodado, ou o processo
	// ainda fechando), a limpeza simplesmente não acontecia e ninguém
	// sabia por quê. Depois veio um retry de 18s — melhorou mas o bug
	// PERSISTIU (reportado de novo): causa real encontrada é a TAREFA
	// AGENDADA disparando uma segunda instância do .exe em background (boot
	// ou 08:00) bem na hora em que o usuário desinstala pela janela aberta —
	// essa segunda instância segura o lock do arquivo por todo o tempo que
	// a coleta leva (pode passar de 1 minuto, bem mais que os 18s de retry),
	// e nunca é fechada por nada que a janela interativa faça, porque é um
	// processo totalmente separado. Duas correções:
	//   1) mata explicitamente qualquer OUTRA instância do mesmo .exe antes
	//      de começar a tentar apagar (RemoverTarefa já impede que uma NOVA
	//      dispare, mas não afeta uma que já esteja rodando).
	//   2) se depois de alguns segundos a pasta ainda não saiu (sinal de que
	//      a própria instância atual, por algum motivo, não fechou rápido o
	//      bastante — webview_go às vezes demora a soltar o processo depois
	//      de Terminate()), força o encerramento dela também pelo PID.
	logErro := filepath.Join(os.TempDir(), "io-nf-automation-desinstalar-erro.log")
	miPID := os.Getpid()
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$pasta = %s
$logErro = %s
$exePath = %s
$miPid = %d

try {
    Get-Process | Where-Object { $_.Id -ne $miPid -and $_.Path -eq $exePath } | Stop-Process -Force -ErrorAction SilentlyContinue
} catch {}

$sucesso = $false
for ($i = 0; $i -lt 25; $i++) {
    Start-Sleep -Seconds 1
    if ($i -eq 5) {
        try { Stop-Process -Id $miPid -Force -ErrorAction SilentlyContinue } catch {}
    }
    try {
        Remove-Item -LiteralPath $pasta -Recurse -Force
        $sucesso = $true
        break
    } catch {
        $ultimoErro = $_.Exception.Message
    }
}
if (-not $sucesso) {
    "$(Get-Date -Format o) — falha ao apagar '$pasta' apos 25 tentativas: $ultimoErro" | Out-File -FilePath $logErro -Append -Encoding utf8
}
`, aspasPS(pastaControle), aspasPS(logErro), aspasPS(exePath), miPID)

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return cmd.Start()
}

func aspasPS(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
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

func copiarArquivo(origem, destino string) error {
	src, err := os.Open(origem)
	if err != nil {
		return fmt.Errorf("abrir %s: %w", filepath.Base(origem), err)
	}
	defer src.Close()

	dst, err := os.Create(destino)
	if err != nil {
		return fmt.Errorf("criar %s: %w", filepath.Base(destino), err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copiar %s: %w", filepath.Base(origem), err)
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

func diagnosticoNFSe(resumo coletansfe.Resumo) string {
	if resumo.Paginas == 0 {
		return "NFS-e: a consulta ao ADN falhou antes de completar qualquer página. Isso é problema de comunicação/TLS com o serviço nacional, não significa ausência de nota. Não clique repetidamente; guarde o log e rode o teste técnico de comunicação."
	}

	totalUtil := resumo.Recebidas + resumo.Emitidas + resumo.Cancelamentos
	partes := []string{
		fmt.Sprintf("NFS-e: ambiente=%s", valorOuTraco(resumo.TipoAmbiente)),
		fmt.Sprintf("status=%s", valorOuTraco(resumo.StatusProcessamento)),
		fmt.Sprintf("recebidas=%d", resumo.Recebidas),
		fmt.Sprintf("emitidas=%d", resumo.Emitidas),
		fmt.Sprintf("cancelamentos=%d", resumo.Cancelamentos),
		fmt.Sprintf("checkpoint=%d", resumo.NSU),
	}
	if resumo.Outras > 0 {
		partes = append(partes, fmt.Sprintf("%d XML(s) ignorado(s) porque o CNPJ do XML não bateu com o CNPJ configurado", resumo.Outras))
	}
	if resumo.OutrosTipos > 0 {
		partes = append(partes, fmt.Sprintf("%d documento(s) de tipo não-NFSe ignorado(s)", resumo.OutrosTipos))
	}
	if len(resumo.Alertas) > 0 {
		partes = append(partes, "alertas="+strings.Join(resumo.Alertas, " | "))
	}
	if len(resumo.ErrosAPI) > 0 {
		partes = append(partes, "erros ADN="+strings.Join(resumo.ErrosAPI, " | "))
	}

	if totalUtil == 0 && resumo.Outras == 0 && resumo.OutrosTipos == 0 && resumo.Erros == 0 {
		return strings.Join(partes, "; ") + ". Nenhuma NFS-e nova veio para este CNPJ/ambiente/checkpoint."
	}
	return strings.Join(partes, "; ") + "."
}

func valorOuTraco(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	return s
}

func respostaOK(msg string) string {
	b, _ := json.Marshal(map[string]any{"ok": true, "mensagem": msg})
	return string(b)
}

func respostaErro(msg string) string {
	b, _ := json.Marshal(map[string]any{"ok": false, "erro": msg})
	return string(b)
}

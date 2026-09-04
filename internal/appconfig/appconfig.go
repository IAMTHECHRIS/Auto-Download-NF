// Package appconfig cuida da configuração da máquina onde o programa roda.
// Na primeira execução, pergunta interativamente (pasta de saída,
// certificado, senha) e salva num config.json ao lado do executável — nas
// próximas vezes só lê o arquivo, sem perguntar nada (necessário pra rodar
// sozinho via agendador de tarefas do Windows).
package appconfig

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	CNPJ             string `json:"cnpj"`
	CUFAutor         string `json:"cUFAutor"` // código IBGE do estado (SP=35)
	CertificadoPfx   string `json:"certificado_pfx"`
	CertificadoSenha string `json:"certificado_senha"`
	// PastaSaida é onde os XMLs/PDFs chegam — de PROPÓSITO fora de
	// qualquer pasta sincronizada (OneDrive/Google Drive/Dropbox). É uma
	// área de chegada: o usuário confere o que baixou e decide o que
	// mover pra pasta sincronizada de verdade. Nunca gravar direto numa
	// pasta de sync — perde a noção do que é novo.
	PastaSaida string `json:"pasta_saida"`
	// AutoBuscarAoAbrir controla se o painel busca notas novas sozinho
	// assim que abre. Zero value (false) É de propósito o padrão — abrir o
	// painel só pra olhar/mexer em configuração não deveria disparar
	// chamada nenhuma na API da SEFAZ. O usuário liga isso explicitamente
	// pelo botão na aba Notas quando quiser.
	AutoBuscarAoAbrir bool `json:"auto_buscar_ao_abrir"`
	// Ambiente escolhe entre os webservices de PRODUÇÃO (dado real, conta
	// pra cota/"Consumo Indevido" de verdade) e HOMOLOGAÇÃO (dado fictício
	// da própria SEFAZ, pra testar mudanças no programa sem risco). Zero
	// value ("") é de propósito produção — configs antigas sem esse campo
	// continuam se comportando exatamente como hoje. Ver EhHomologacao() e
	// PastaEfetiva().
	Ambiente string `json:"ambiente"`

	EmailAtivo   bool   `json:"email_ativo"`
	EmailSMTP    string `json:"email_smtp"`
	EmailPorta   int    `json:"email_porta"`
	EmailUsuario string `json:"email_usuario"`
	EmailSenha   string `json:"email_senha"`
	EmailDe      string `json:"email_de"`
	EmailPara    string `json:"email_para"`
}

const (
	AmbienteProducao    = "producao"
	AmbienteHomologacao = "homologacao"
)

// EhHomologacao diz se essa config está apontando pro ambiente de teste da
// SEFAZ (dado fictício, endpoints "hom"/"producaorestrita").
func (c Config) EhHomologacao() bool {
	return c.Ambiente == AmbienteHomologacao
}

// TpAmb é o valor que entra no XML (<tpAmb>) mandado pra SEFAZ: 1=produção,
// 2=homologação — mesmo código em qualquer webservice do padrão nacional.
func (c Config) TpAmb() string {
	if c.EhHomologacao() {
		return "2"
	}
	return "1"
}

// PastaEfetiva é a pasta que os coletores e o catálogo devem usar de
// verdade — em homologação, é uma SUBPASTA isolada de PastaSaida
// ("_HOMOLOGACAO"), nunca a pasta principal. Isso garante que dado de teste
// (fictício) nunca se mistura com nota fiscal real, mesmo que o usuário
// esqueça de escolher uma pasta diferente ao trocar de ambiente — a
// separação é automática, não depende de disciplina manual.
func (c Config) PastaEfetiva() string {
	if c.EhHomologacao() {
		return filepath.Join(c.PastaSaida, "_HOMOLOGACAO")
	}
	return c.PastaSaida
}

// PastaControle é onde ficam o executável, config.json, catálogo,
// checkpoints e logs — tudo que NÃO é nota fiscal. Fica separado numa
// subpasta própria (nome com "_" na frente pra ordenar primeiro no
// Explorer) pra não misturar arquivo técnico com nfe-compras/nfse na raiz
// da pasta que o usuário escolheu pras notas.
func PastaControle(pastaSaida string) string {
	return filepath.Join(pastaSaida, "_Controle")
}

// configPath usa a PASTA ATUAL (não o caminho do .exe) — no Windows, dar
// duplo-clique num .exe já deixa o diretório atual igual à pasta dele, e
// aqui no desenvolvimento (rodando com "go run") isso também funciona
// direto, sem precisar de exe fixo.
// Existe diz se já existe um config.json salvo — usado por quem quiser
// decidir COMO configurar (janela gráfica, assistente de texto, etc.)
// antes de chamar Load().
func Existe() bool {
	_, err := os.Stat(configPath())
	return err == nil
}

func configPath() string {
	wd, err := os.Getwd()
	if err != nil {
		return "config.json"
	}
	return filepath.Join(wd, "config.json")
}

// CaminhoArquivo expõe o caminho do config.json pra quem precisar apagá-lo
// de propósito (ex: botão "Reconfigurar" do painel).
func CaminhoArquivo() string {
	return configPath()
}

// Load lê o config.json ao lado do executável. Se não existir, roda o
// assistente interativo (Setup) e salva antes de devolver.
func Load() (Config, error) {
	path := configPath()

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if !terminalInterativo() {
			return Config{}, fmt.Errorf(
				"config.json não existe em %s, e não há terminal interativo pra perguntar "+
					"(rodando via agendador/timer?). Rode manualmente uma vez primeiro pra criar o config.json",
				path,
			)
		}
		cfg, err := Setup()
		if err != nil {
			return Config{}, err
		}
		if err := Save(cfg); err != nil {
			return Config{}, fmt.Errorf("salvar config: %w", err)
		}
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("ler config %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("config.json inválido: %w", err)
	}
	return cfg, nil
}

func Save(cfg Config) error {
	return SalvarEm(filepath.Dir(configPath()), cfg)
}

// SalvarEm grava o config.json numa pasta específica — usado pelo
// relocador na primeira instalação, quando o processo ainda está rodando
// da pasta de Downloads mas o config.json precisa nascer já na pasta
// instalada (não dá pra usar Save(), que sempre grava no diretório atual
// do processo).
func SalvarEm(dir string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600)
}

// Setup pergunta interativamente no terminal e monta a config. Chamado só
// na primeira execução (quando config.json ainda não existe).
func Setup() (Config, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("=== Primeira execução — configuração ===")
	fmt.Println()

	fmt.Print("CNPJ da empresa (só números, sem pontuação): ")
	cnpj := readLine(reader)
	for cnpj == "" {
		fmt.Print("  (obrigatório) CNPJ da empresa: ")
		cnpj = readLine(reader)
	}

	fmt.Print("Código IBGE do estado (SP=35) [35]: ")
	cuf := readLine(reader)
	if cuf == "" {
		cuf = "35"
	}

	fmt.Print("Caminho completo do certificado .pfx: ")
	pfx := readLine(reader)
	for pfx == "" {
		fmt.Print("  (obrigatório) Caminho completo do certificado .pfx: ")
		pfx = readLine(reader)
	}

	fmt.Print("Senha do certificado: ")
	senha := readLine(reader)

	fmt.Println()
	fmt.Println("Pasta onde as notas vão ser salvas.")
	fmt.Println("IMPORTANTE: escolha uma pasta LOCAL, que NÃO sincroniza com")
	fmt.Println("nuvem (OneDrive/Google Drive/Dropbox). Essa é uma área de")
	fmt.Println("chegada — depois de conferir, você move manualmente pra")
	fmt.Println("pasta sincronizada de verdade.")
	fmt.Print("Pasta de saída (ex: C:\\NotasFiscais\\entrada): ")
	pasta := readLine(reader)
	for pasta == "" {
		fmt.Print("  (obrigatório) Pasta de saída: ")
		pasta = readLine(reader)
	}

	if err := os.MkdirAll(pasta, 0o755); err != nil {
		return Config{}, fmt.Errorf("criar pasta %s: %w", pasta, err)
	}

	cfg := Config{
		CNPJ:             cnpj,
		CUFAutor:         cuf,
		CertificadoPfx:   pfx,
		CertificadoSenha: senha,
		PastaSaida:       pasta,
	}

	fmt.Println()
	fmt.Println("Configuração salva em config.json (ao lado do programa).")
	fmt.Println("Da próxima vez não vai perguntar de novo — pra mudar algo,")
	fmt.Println("edite o config.json diretamente ou apague pra refazer.")
	fmt.Println()

	return cfg, nil
}

func readLine(r *bufio.Reader) string {
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(line)
}

// terminalInterativo checa se stdin é um terminal de verdade (não um
// agendador/timer sem tty). Não é 100% infalível em todo SO, mas evita o
// caso comum de travar pra sempre esperando um Enter que nunca vem.
func terminalInterativo() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// Programa único: coleta NFe de compra + NFSe, se auto-instala numa pasta
// estável (junto dos XMLs/logs) e se auto-agenda no Windows (sem precisar
// de ninguém configurando Agendador de Tarefas manualmente) e mostra um
// painel com o que já foi baixado quando alguém abre manualmente.
package main

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"sieg-automation/internal/appconfig"
	"sieg-automation/internal/coletanfe"
	"sieg-automation/internal/coletansfe"
	"sieg-automation/internal/instalador"
	"sieg-automation/internal/painel"
	"sieg-automation/internal/relocador"
	"sieg-automation/internal/wintask"
)

// rodandoViaAgendador é true quando o próprio wintask.EnsureDailyTask
// invoca o .exe sozinho (agendado ou ao ligar o PC) — nesse caso não existe
// ninguém na frente da tela pra ver um painel, então roda só a coleta e
// sai. Sem essa flag, abrir o .exe manualmente mostra o painel.
func rodandoViaAgendador() bool {
	for _, arg := range os.Args[1:] {
		if arg == "--agendado" {
			return true
		}
	}
	return false
}

func main() {
	// primeira execução: abre a janela gráfica de configuração (Windows) em
	// vez do assistente de texto no terminal — mais amigável pra quem não
	// mexe com linha de comando no dia a dia.
	if !appconfig.Existe() && runtime.GOOS == "windows" {
		if !instalador.Executar() {
			log.Println("Configuração não foi concluída — encerrando.")
			return
		}
		if encerrarAqui() {
			return
		}
	}

	for {
		cfg, err := appconfig.Load()
		if err != nil {
			log.Fatalf("carregar configuração: %v", err)
		}

		if rodandoViaAgendador() || runtime.GOOS != "windows" {
			// aqui não tem janela nenhuma esperando — pode registrar a
			// tarefa de forma síncrona sem incomodar ninguém.
			garantirTarefaAgendada()
			rodarColeta(cfg)
			return
		}

		// duplo-clique manual: registra a tarefa agendada EM PARALELO — na
		// primeira vez isso chama o PowerShell (Register-ScheduledTask), que
		// demora um pouco. Se isso rodasse antes de abrir a janela, a janela
		// só apareceria depois de 1-2s parada, parecendo travada. Rodando em
		// goroutine, a janela do painel aparece na hora e o registro
		// acontece por trás, sem o usuário perceber.
		go garantirTarefaAgendada()

		// mostra o painel (o próprio painel decide se busca notas sozinho,
		// baseado em cfg.AutoBuscarAoAbrir).
		querReconfigurar, err := painel.Abrir(cfg)
		if err != nil {
			log.Printf("painel: %v", err)
			return
		}
		if !querReconfigurar {
			return
		}
		if err := os.Remove(appconfig.CaminhoArquivo()); err != nil {
			log.Printf("apagar configuração antiga: %v", err)
			return
		}
		if !instalador.Executar() {
			return
		}
		if encerrarAqui() {
			return
		}
		// volta pro topo do loop: recarrega a config nova e reabre o painel
	}
}

// encerrarAqui roda logo depois de QUALQUER instalador.Executar() bem
// sucedido (primeira configuração ou reconfiguração via painel). Se o
// executável atual não está dentro da pasta de notas ainda, copia ele pra
// lá, relança a cópia instalada, e devolve true — quem chamou deve
// encerrar o processo atual na hora, porque quem continua é o processo
// novo. Se já está no lugar certo (ou algo falhou na cópia), devolve false
// e a execução continua normalmente de onde está.
func encerrarAqui() bool {
	if runtime.GOOS != "windows" {
		return false
	}

	cfg, err := appconfig.Load()
	if err != nil {
		log.Printf("instalação: não consegui reler a configuração recém-salva: %v", err)
		return false
	}

	precisa, err := relocador.NecessarioRelocar(cfg)
	if err != nil {
		log.Printf("instalação: %v — continuando de onde está", err)
		return false
	}
	if !precisa {
		return false
	}

	novoExe, err := relocador.Relocar(cfg)
	if err != nil {
		log.Printf("instalação: não consegui copiar o programa pra pasta de notas: %v", err)
		log.Println("(continuando de onde está — funciona, só não fica junto dos XMLs)")
		return false
	}

	if err := relocador.Relancar(novoExe); err != nil {
		log.Printf("instalação: copiei pra %s mas não consegui reabrir de lá: %v", novoExe, err)
		log.Println("Abra manualmente o programa nesse caminho a partir de agora.")
		return false
	}

	log.Printf("Instalado em: %s", novoExe)
	log.Println("Pode apagar o arquivo que você baixou (ex: da pasta Downloads) —")
	log.Println("a partir de agora use o que ficou no caminho acima.")

	// a configuração antiga (na pasta de onde rodou, ex: Downloads) não
	// serve mais — se sobrar lá, um duplo-clique futuro nesse .exe antigo
	// vai achar "já configurado" só com dado velho. Apagando, ele volta a
	// mostrar o assistente (e se reinstala de novo, de forma idempotente).
	_ = os.Remove(appconfig.CaminhoArquivo())

	return true
}

// garantirTarefaAgendada só faz sentido no Windows; em outros sistemas não
// faz nada (ver internal/wintask).
func garantirTarefaAgendada() {
	if err := wintask.EnsureDailyTask("08:00"); err != nil {
		log.Printf("aviso: não consegui criar a tarefa agendada automaticamente: %v", err)
		log.Println("(a coleta de hoje continua normalmente mesmo assim — só não ficou agendada sozinha)")
	}
}

func rodarColeta(cfg appconfig.Config) {
	// trava de "1x por dia": com o gatilho de boot (além do diário às 08h),
	// o Windows pode disparar esse processo mais de uma vez no mesmo dia
	// (ex: PC reiniciou de tarde por causa de update). Sem essa trava, cada
	// disparo chamaria a API de novo à toa — não é o "Consumo Indevido"
	// (esse vem de reiniciar o NSU do zero), mas é uma chamada desnecessária
	// mesmo assim.
	marcador := filepath.Join(cfg.PastaSaida, ".ultima-coleta-sucesso")
	hoje := time.Now().Format("2006-01-02")

	if dados, err := os.ReadFile(marcador); err == nil && strings.TrimSpace(string(dados)) == hoje {
		log.Println("Já rodou hoje (provavelmente por causa do gatilho de boot) — não repete a coleta.")
		log.Println("Pra forçar mesmo assim, abra o programa e use 'Buscar notas agora' no painel.")
		return
	}

	if err := coletanfe.Run(cfg); err != nil {
		log.Printf("coleta de NFe: %v", err)
	}

	if err := coletansfe.Run(cfg); err != nil {
		log.Printf("coleta de NFSe: %v", err)
	}

	if err := os.WriteFile(marcador, []byte(hoje), 0o644); err != nil {
		log.Printf("aviso: não consegui gravar marcador de última coleta: %v", err)
	}
}

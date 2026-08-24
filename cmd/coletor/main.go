// Programa único: coleta NFe de compra + NFSe, se auto-agenda no Windows
// (sem precisar de ninguém configurando Agendador de Tarefas manualmente) e
// mostra um painel com o que já foi baixado quando alguém abre manualmente.
package main

import (
	"log"
	"os"
	"runtime"

	"sieg-automation/internal/appconfig"
	"sieg-automation/internal/coletanfe"
	"sieg-automation/internal/coletansfe"
	"sieg-automation/internal/instalador"
	"sieg-automation/internal/painel"
	"sieg-automation/internal/wintask"
)

// rodandoViaAgendador é true quando o próprio wintask.EnsureDailyTask
// invoca o .exe sozinho às 08h — nesse caso não existe ninguém na frente
// da tela pra ver um painel, então roda só a coleta e sai. Sem essa flag,
// abrir o .exe manualmente mostra o painel.
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
	}

	for {
		cfg, err := appconfig.Load()
		if err != nil {
			log.Fatalf("carregar configuração: %v", err)
		}

		// só faz sentido no Windows; em outros sistemas não faz nada (ver
		// internal/wintask).
		if err := wintask.EnsureDailyTask("08:00"); err != nil {
			log.Printf("aviso: não consegui criar a tarefa agendada automaticamente: %v", err)
			log.Println("(a coleta de hoje continua normalmente mesmo assim — só não ficou agendada sozinha)")
		}

		if rodandoViaAgendador() || runtime.GOOS != "windows" {
			rodarColeta(cfg)
			return
		}

		// duplo-clique manual: mostra o painel (que já dispara uma busca em
		// segundo plano assim que abre).
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
		// volta pro topo do loop: recarrega a config nova e reabre o painel
	}
}

func rodarColeta(cfg appconfig.Config) {
	if err := coletanfe.Run(cfg); err != nil {
		log.Printf("coleta de NFe: %v", err)
	}

	if err := coletansfe.Run(cfg); err != nil {
		log.Printf("coleta de NFSe: %v", err)
	}
}

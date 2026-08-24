// Programa único: coleta NFe de compra + NFSe, e se auto-agenda no Windows
// (sem precisar de ninguém configurando Agendador de Tarefas manualmente).
package main

import (
	"log"
	"runtime"

	"sieg-automation/internal/appconfig"
	"sieg-automation/internal/coletanfe"
	"sieg-automation/internal/coletansfe"
	"sieg-automation/internal/instalador"
	"sieg-automation/internal/wintask"
)

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

	if err := coletanfe.Run(cfg); err != nil {
		log.Printf("coleta de NFe: %v", err)
	}

	if err := coletansfe.Run(cfg); err != nil {
		log.Printf("coleta de NFSe: %v", err)
	}
}

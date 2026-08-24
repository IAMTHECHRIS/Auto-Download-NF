// Package wintask cadastra o próprio executável no Agendador de Tarefas do
// Windows, pra rodar sozinho todo dia — sem depender de alguém (TI,
// programador) configurar isso manualmente depois. Só funciona no Windows;
// em outros sistemas, EnsureDailyTask não faz nada (retorna nil).
package wintask

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

const nomeTarefa = "ColetaNotasFiscaisAutomatica"

// EnsureDailyTask garante que existe uma tarefa agendada rodando este
// executável todo dia no horário informado (formato "HH:MM"). Se a tarefa
// já existe, não faz nada (idempotente — seguro chamar toda execução).
func EnsureDailyTask(horario string) error {
	if runtime.GOOS != "windows" {
		return nil // no-op fora do Windows
	}

	if existeTarefa() {
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("descobrir caminho do executável: %w", err)
	}

	cmd := exec.Command("schtasks",
		"/Create",
		"/SC", "DAILY",
		"/TN", nomeTarefa,
		"/TR", fmt.Sprintf(`"%s"`, exe),
		"/ST", horario,
		"/F", // sobrescreve sem perguntar, se por algum motivo já existir mas existeTarefa() não pegou
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("criar tarefa agendada: %w — saída: %s", err, string(out))
	}

	fmt.Printf("Tarefa agendada criada: roda sozinho todo dia às %s.\n", horario)
	fmt.Println("(Não precisa de ninguém configurando isso manualmente — já está pronto.)")

	return nil
}

func existeTarefa() bool {
	cmd := exec.Command("schtasks", "/Query", "/TN", nomeTarefa)
	return cmd.Run() == nil
}

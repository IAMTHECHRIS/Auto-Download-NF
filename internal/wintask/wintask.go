// Package wintask cadastra o próprio executável no Agendador de Tarefas do
// Windows, pra rodar sozinho todo dia — sem depender de alguém (TI,
// programador) configurar isso manualmente depois. Só funciona no Windows;
// em outros sistemas, EnsureDailyTask não faz nada (retorna nil).
package wintask

import "runtime"

const nomeTarefa = "ColetaNotasFiscaisAutomatica"

// EnsureDailyTask garante que existe uma tarefa agendada rodando este
// executável todo dia no horário informado (formato "HH:MM"), com "rodar
// assim que possível" ligado: se o PC estiver desligado (ou o usuário
// deslogado) nesse horário, a tarefa roda na próxima vez que ele
// ligar/entrar, em vez de simplesmente pular o dia. Idempotente — seguro
// chamar toda execução, só cria na primeira vez.
func EnsureDailyTask(horario string) error {
	if runtime.GOOS != "windows" {
		return nil // no-op fora do Windows
	}
	return garantirTarefa(horario)
}

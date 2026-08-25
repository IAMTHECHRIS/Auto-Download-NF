//go:build !windows

// Stub pra sistemas que não são Windows — o Agendador de Tarefas só existe
// lá (no Linux/Hetzner, a coleta diária roda via systemd timer, fora deste
// pacote).
package wintask

func garantirTarefa(horario string) error {
	return nil
}

func removerTarefa() error {
	return nil
}

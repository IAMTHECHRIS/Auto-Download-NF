//go:build !windows

// Stub pra sistemas que não são Windows — o painel gráfico só existe lá
// (mesmo motivo do internal/instalador).
package painel

import "io-nf-automation/internal/appconfig"

func Abrir(cfg appconfig.Config) (bool, error) {
	return false, nil
}

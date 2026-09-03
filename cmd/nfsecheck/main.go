package main

import (
	"fmt"
	"os"

	"io-nf-automation/internal/appconfig"
	"io-nf-automation/internal/coletansfe"
)

func main() {
	cfg, err := appconfig.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "CONFIG_ERRO: %v\n", err)
		os.Exit(1)
	}

	resumo, err := coletansfe.Run(cfg)
	fmt.Printf("RESUMO_NFSE: paginas=%d recebidas=%d emitidas=%d cancelamentos=%d semRef=%d outras=%d outrosTipos=%d erros=%d nsu=%d ambiente=%s status=%s alertas=%v errosADN=%v tipos=%v limitePaginas=%v\n",
		resumo.Paginas,
		resumo.Recebidas,
		resumo.Emitidas,
		resumo.Cancelamentos,
		resumo.SemReferencia,
		resumo.Outras,
		resumo.OutrosTipos,
		resumo.Erros,
		resumo.NSU,
		resumo.TipoAmbiente,
		resumo.StatusProcessamento,
		resumo.Alertas,
		resumo.ErrosAPI,
		resumo.TiposDocumento,
		resumo.ParouPorLimitePaginas,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "NFSE_ERRO: %v\n", err)
		os.Exit(1)
	}
}

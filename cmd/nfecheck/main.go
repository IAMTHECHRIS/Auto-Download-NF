package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"io-nf-automation/internal/appconfig"
	"io-nf-automation/internal/nfedist"
)

func main() {
	cfg, err := appconfig.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "CONFIG_ERRO: %v\n", err)
		os.Exit(1)
	}

	checkpointPath := filepath.Join(appconfig.PastaControle(cfg.PastaEfetiva()), ".checkpoint-nfedist-nsu")
	nsu, err := nfedist.LerCheckpoint(checkpointPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "CHECKPOINT_ERRO: %v\n", err)
		os.Exit(1)
	}

	client, err := nfedist.NewClient(cfg.CertificadoPfx, cfg.CertificadoSenha, cfg.TpAmb())
	if err != nil {
		fmt.Fprintf(os.Stderr, "CLIENTE_ERRO: %v\n", err)
		os.Exit(1)
	}

	res, err := client.BuscarLote(cfg.CUFAutor, cfg.CNPJ, nsu)
	if err != nil {
		fmt.Fprintf(os.Stderr, "NFE_ERRO: %v\n", err)
		os.Exit(1)
	}

	nsuRetornado, convErr := strconv.Atoi(res.UltNSU)
	if convErr == nil && nsuRetornado > nsu {
		if err := nfedist.SalvarCheckpoint(checkpointPath, nsuRetornado); err != nil {
			fmt.Fprintf(os.Stderr, "CHECKPOINT_SALVAR_ERRO: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Printf("RESUMO_NFE_UMA_CHAMADA: checkpoint_inicial=%d cStat=%s motivo=%q docs=%d ultNSU=%s maxNSU=%s checkpoint_salvo=%t\n",
		nsu, res.CStat, res.XMotivo, len(res.Docs), res.UltNSU, res.MaxNSU, convErr == nil && nsuRetornado > nsu)
}

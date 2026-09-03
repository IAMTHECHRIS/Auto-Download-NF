// Package relocador resolve o problema de o .exe ficar "desconexo" de onde
// moram os XMLs/logs: na primeira configuração bem-sucedida, o programa
// ainda está rodando de onde foi baixado (normalmente a pasta Downloads).
// Se ficasse lá, a tarefa agendada apontaria pra um arquivo que pode sumir
// se a pasta Downloads for limpa um dia.
//
// Solução (mesmo padrão de qualquer instalador — Steam, etc.): copia o
// próprio executável pra dentro da pasta de notas escolhida (numa
// subpasta "programa"), grava o config.json lá, relança a cópia instalada
// e encerra o processo original. Dali em diante, config.json, catalogo.csv
// e o .exe moram juntos, num lugar estável — nunca mais em Downloads.
package relocador

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"io-nf-automation/internal/appconfig"
)

const nomeExeInstalado = "coletor-notas-fiscais.exe"

// PastaInstalada devolve onde o programa deveria morar pra essa
// configuração — a mesma pasta "_Controle" onde ficam config.json,
// catálogo, checkpoints e logs (ver appconfig.PastaControle).
func PastaInstalada(cfg appconfig.Config) string {
	return appconfig.PastaControle(cfg.PastaSaida)
}

// NecessarioRelocar diz se o executável ATUAL (o que está rodando agora)
// já está na pasta instalada, ou se ainda precisa ser copiado pra lá.
func NecessarioRelocar(cfg appconfig.Config) (bool, error) {
	exeAtual, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("descobrir caminho do executável atual: %w", err)
	}

	dirAtual, err := filepath.Abs(filepath.Dir(exeAtual))
	if err != nil {
		return false, err
	}
	dirInstalado, err := filepath.Abs(PastaInstalada(cfg))
	if err != nil {
		return false, err
	}

	return !strings.EqualFold(dirAtual, dirInstalado), nil
}

// Relocar copia o executável atual pra dentro da pasta instalada e grava
// uma cópia do config.json lá. Se a pasta já existia, trata como atualização
// segura: preserva catálogo/checkpoints e faz backup dos arquivos de
// controle sensíveis antes de sobrescrever o .exe/config.
func Relocar(cfg appconfig.Config) (novoExe string, err error) {
	exeAtual, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("descobrir caminho do executável atual: %w", err)
	}

	pastaInstalada := PastaInstalada(cfg)
	instalacaoExistente := pastaExiste(pastaInstalada)
	if err := os.MkdirAll(pastaInstalada, 0o755); err != nil {
		return "", fmt.Errorf("criar pasta do programa: %w", err)
	}

	if instalacaoExistente {
		if err := backupControleAntesDeAtualizar(pastaInstalada); err != nil {
			return "", fmt.Errorf("backup de segurança antes da atualização: %w", err)
		}
	}

	novoExe = filepath.Join(pastaInstalada, nomeExeInstalado)
	if err := copiarArquivo(exeAtual, novoExe); err != nil {
		return "", fmt.Errorf("copiar executável: %w", err)
	}

	if err := appconfig.SalvarEm(pastaInstalada, cfg); err != nil {
		return "", fmt.Errorf("salvar configuração na pasta instalada: %w", err)
	}

	return novoExe, nil
}

func pastaExiste(caminho string) bool {
	info, err := os.Stat(caminho)
	return err == nil && info.IsDir()
}

func backupControleAntesDeAtualizar(pastaInstalada string) error {
	backupDir := filepath.Join(pastaInstalada, "_backup_atualizacao_"+time.Now().Format("20060102_150405"))
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return err
	}

	arquivos := []string{
		"config.json",
		"catalogo.csv",
		".checkpoint-nfedist-nsu",
		".checkpoint-adn-nsu",
		"painel-debug.log",
		"instalador-debug.log",
		nomeExeInstalado,
	}
	for _, nome := range arquivos {
		origem := filepath.Join(pastaInstalada, nome)
		if _, err := os.Stat(origem); err != nil {
			continue
		}
		destino := filepath.Join(backupDir, nome)
		if err := copiarArquivo(origem, destino); err != nil {
			return fmt.Errorf("copiar %s para backup: %w", nome, err)
		}
	}
	return os.WriteFile(filepath.Join(backupDir, "LEIA-ME.txt"), []byte(
		"Backup automático criado antes de atualizar o Coletor de Notas Fiscais.\r\n"+
			"Para voltar ao estado anterior, copie os arquivos desta pasta para a pasta _Controle.\r\n"+
			"Catálogo e checkpoints foram apenas copiados; a atualização não apaga esses arquivos.\r\n",
	), 0o644)
}

func copiarArquivo(origem, destino string) error {
	src, err := os.Open(origem)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(destino, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

// Relancar inicia o executável já instalado como um processo novo e
// independente, com o diretório de trabalho correto (pra config.json ser
// achado do jeito que appconfig espera). Quem chama deve encerrar o
// processo atual logo em seguida — a partir daqui, quem continua é o
// processo novo.
func Relancar(novoExe string) error {
	cmd := exec.Command(novoExe)
	cmd.Dir = filepath.Dir(novoExe)
	return cmd.Start()
}

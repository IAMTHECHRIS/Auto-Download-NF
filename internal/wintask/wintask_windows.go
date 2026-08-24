//go:build windows

package wintask

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func garantirTarefa(horario string) error {
	if existeTarefa() {
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("descobrir caminho do executável: %w", err)
	}

	// schtasks /Create básico (usado antes) não expõe "rodar assim que
	// possível" (StartWhenAvailable) — essa opção só existe via XML da
	// tarefa ou via o módulo ScheduledTasks do PowerShell. Uso o PowerShell,
	// mesmo caminho já usado no instalador pros diálogos nativos.
	//
	// --agendado diz pro cmd/coletor que essa execução é automática, sem
	// ninguém na frente do computador — roda só a coleta, sem abrir o
	// painel gráfico.
	script := fmt.Sprintf(`
$ErrorActionPreference = "Stop"
$acao = New-ScheduledTaskAction -Execute %s -Argument '--agendado'
$gatilho = New-ScheduledTaskTrigger -Daily -At '%s'
$config = New-ScheduledTaskSettingsSet -StartWhenAvailable -ExecutionTimeLimit (New-TimeSpan -Hours 2)
$principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType Interactive -RunLevel Limited
Register-ScheduledTask -TaskName %s -Action $acao -Trigger $gatilho -Settings $config -Principal $principal -Description "Coleta diaria de notas fiscais (NFe/NFSe) via SEFAZ. Criada automaticamente." -Force | Out-Null
`, aspasPS(exe), horario, aspasPS(nomeTarefa))

	saida, err := rodarPowerShell(script)
	if err != nil {
		return fmt.Errorf("criar tarefa agendada: %w — saída: %s", err, saida)
	}

	fmt.Printf("Tarefa agendada criada: roda sozinho todo dia às %s.\n", horario)
	fmt.Println("Se o PC estiver desligado (ou você deslogado) nesse horário, ela roda")
	fmt.Println("assim que ligar/entrar de novo — não precisa configurar nada manualmente.")

	return nil
}

func existeTarefa() bool {
	cmd := exec.Command("schtasks", "/Query", "/TN", nomeTarefa)
	return cmd.Run() == nil
}

// aspasPS envolve a string em aspas simples pro PowerShell — '' escapa uma
// aspa simples literal dentro do valor (ex: caminho de arquivo com apóstrofo).
func aspasPS(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func rodarPowerShell(script string) (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	var saida bytes.Buffer
	cmd.Stdout = &saida
	cmd.Stderr = &saida
	err := cmd.Run()
	return saida.String(), err
}

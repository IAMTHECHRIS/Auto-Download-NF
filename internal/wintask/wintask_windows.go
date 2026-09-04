//go:build windows

package wintask

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func garantirTarefa(horario string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("descobrir caminho do executável: %w", err)
	}

	// Antes isso só checava "a tarefa existe?" e parava se sim — bug: depois
	// de reinstalar em outra pasta (ex: durante testes), a tarefa antiga
	// ficava presa apontando pro caminho velho, sem nunca ser corrigida.
	// Agora compara o caminho REGISTRADO na tarefa com o executável atual;
	// se bater, não mexe; se não bater (ou não existir), (re)registra do
	// zero — Register-ScheduledTask -Force sobrescreve sem perguntar.
	registrado, existe := caminhoRegistrado()
	if existe && strings.EqualFold(filepath.Clean(registrado), filepath.Clean(exe)) {
		return nil
	}

	// schtasks /Create básico (usado antes) não expõe "rodar assim que
	// possível" (StartWhenAvailable) — essa opção só existe via XML da
	// tarefa ou via o módulo ScheduledTasks do PowerShell. Uso o PowerShell,
	// mesmo caminho já usado no instalador pros diálogos nativos.
	//
	// DOIS gatilhos: o diário às HH:MM (caso normal, PC já ligado) e um de
	// boot com 2 min de atraso — cobre o caso de o PC ligar bem depois do
	// horário (StartWhenAvailable já ajuda nisso, mas o gatilho de boot dá
	// um horário mais previsível, sem depender só da recuperação do
	// Windows). O atraso de 2 min existe pra deixar rede/sistema
	// assentarem antes de tentar falar com a SEFAZ. A trava de "só 1x por
	// dia" fica no próprio programa (cmd/coletor/main.go), não aqui — dois
	// gatilhos no mesmo dia (ex: reinício de tarde por update) não devem
	// gerar duas coletas.
	//
	// --agendado diz pro cmd/coletor que essa execução é automática, sem
	// ninguém na frente do computador — roda só a coleta, sem abrir o
	// painel gráfico.
	script := fmt.Sprintf(`
$ErrorActionPreference = "Stop"
$acao = New-ScheduledTaskAction -Execute %s -Argument '--agendado'
$gatilhoDiario = New-ScheduledTaskTrigger -Daily -At '%s'
$gatilhoBoot = New-ScheduledTaskTrigger -AtStartup
$gatilhoBoot.Delay = 'PT2M'
$config = New-ScheduledTaskSettingsSet -StartWhenAvailable -ExecutionTimeLimit (New-TimeSpan -Hours 2)
$principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType Interactive -RunLevel Limited
Register-ScheduledTask -TaskName %s -Action $acao -Trigger @($gatilhoDiario, $gatilhoBoot) -Settings $config -Principal $principal -Description "Coleta diaria de notas fiscais (NFe/NFSe) via SEFAZ. Criada automaticamente." -Force | Out-Null
`, aspasPS(exe), horario, aspasPS(nomeTarefa))

	saida, err := rodarPowerShell(script)
	if err != nil {
		return fmt.Errorf("criar tarefa agendada: %w — saída: %s", err, saida)
	}

	if existe {
		fmt.Println("Tarefa agendada estava desatualizada (apontava pra outro lugar) — corrigida.")
	} else {
		fmt.Printf("Tarefa agendada criada: roda sozinho todo dia às %s.\n", horario)
	}
	fmt.Println("Se o PC estiver desligado (ou você deslogado) nesse horário, ela roda")
	fmt.Println("assim que ligar/entrar de novo — não precisa configurar nada manualmente.")

	return nil
}

// caminhoRegistrado devolve o executável que a tarefa (se existir) está
// configurada pra rodar, e se ela existe. String vazia + false = não existe.
func caminhoRegistrado() (string, bool) {
	saida, err := rodarPowerShell(fmt.Sprintf(`
$t = Get-ScheduledTask -TaskName %s -ErrorAction SilentlyContinue
if ($t) { Write-Output $t.Actions[0].Execute }
`, aspasPS(nomeTarefa)))
	if err != nil {
		return "", false
	}
	caminho := strings.Trim(strings.TrimSpace(saida), `"`)
	if caminho == "" {
		return "", false
	}
	return caminho, true
}

func statusTarefa() (string, bool, error) {
	saida, err := rodarPowerShell(fmt.Sprintf(`
$t = Get-ScheduledTask -TaskName %s -ErrorAction SilentlyContinue
if (-not $t) { exit 0 }
$info = Get-ScheduledTaskInfo -TaskName %s
Write-Output ("Execute=" + $t.Actions[0].Execute)
Write-Output ("Arguments=" + $t.Actions[0].Arguments)
Write-Output ("State=" + $t.State)
Write-Output ("LastRunTime=" + $info.LastRunTime)
Write-Output ("LastTaskResult=" + $info.LastTaskResult)
Write-Output ("NextRunTime=" + $info.NextRunTime)
`, aspasPS(nomeTarefa), aspasPS(nomeTarefa)))
	if err != nil {
		return "", false, err
	}
	saida = strings.TrimSpace(saida)
	return saida, saida != "", nil
}

func removerTarefa() error {
	// Checa antes via Get-ScheduledTask (mesma função que já uso pra achar
	// o caminho registrado) em vez de tentar reconhecer a mensagem de erro
	// do "schtasks /Delete" quando a tarefa não existe — essa mensagem
	// muda de texto entre versões/idioma do Windows (chegou a aparecer
	// como "O sistema não pode encontrar o arquivo especificado", bem
	// diferente do que eu esperava) e o console do Windows pode embaralhar
	// os acentos na captura, então casar substring é frágil. Perguntar
	// direto se existe é mais confiável.
	if _, existe := caminhoRegistrado(); !existe {
		return nil
	}

	cmd := exec.Command("schtasks", "/Delete", "/TN", nomeTarefa, "/F")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("remover tarefa agendada: %w — saída: %s", err, string(out))
	}
	return nil
}

// aspasPS envolve a string em aspas simples pro PowerShell — ” escapa uma
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

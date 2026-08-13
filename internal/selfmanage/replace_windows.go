//go:build windows

package selfmanage

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func replaceExecutable(target, staged string) error {
	helper, err := writeWindowsHelper(filepath.Dir(target), os.Getpid(), target, staged, false, "")
	if err != nil {
		return err
	}
	command := helperCommand(helper)
	if err := configureDetached(command); err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("%w: 启动 Windows 更新 helper: %v", ErrState, err)
	}
	return nil
}

func writeWindowsHelper(directory string, _ int, target, staged string, removeOnly bool, purgeRoot string) (string, error) {
	helper := filepath.Join(directory, fmt.Sprintf(".jkv-helper-%d.cmd", os.Getpid()))
	mode := "replace"
	if removeOnly {
		mode = "remove"
	}
	script := "@echo off\r\n" +
		"setlocal\r\n" +
		"set \"TARGET=" + cmdValue(target) + "\"\r\n" +
		"set \"STAGED=" + cmdValue(staged) + "\"\r\n" +
		"set \"BACKUP=" + cmdValue(target+".old") + "\"\r\n" +
		"set \"PURGE=" + cmdValue(purgeRoot) + "\"\r\n" +
		"set ATTEMPTS=0\r\n" +
		"if \"" + mode + "\"==\"remove\" goto remove\r\n" +
		":backup\r\n" +
		"del /F /Q \"%BACKUP%\" >NUL 2>&1\r\n" +
		"move /Y \"%TARGET%\" \"%BACKUP%\" >NUL && goto install\r\n" +
		"set /a ATTEMPTS+=1\r\n" +
		"if %ATTEMPTS% GEQ 60 goto fail\r\n" +
		"ping 127.0.0.1 -n 2 >NUL\r\n" +
		"goto backup\r\n" +
		":install\r\n" +
		"move /Y \"%STAGED%\" \"%TARGET%\" >NUL && goto cleanup\r\n" +
		"set ATTEMPTS=0\r\n" +
		":restore\r\n" +
		"move /Y \"%BACKUP%\" \"%TARGET%\" >NUL && goto fail\r\n" +
		"set /a ATTEMPTS+=1\r\n" +
		"if %ATTEMPTS% GEQ 60 goto fail\r\n" +
		"ping 127.0.0.1 -n 2 >NUL\r\n" +
		"goto restore\r\n" +
		":remove\r\n" +
		"set ATTEMPTS=0\r\n" +
		":delete\r\n" +
		"del /F /Q \"%TARGET%\" >NUL 2>&1\r\n" +
		"if not exist \"%TARGET%\" goto removed\r\n" +
		"set /a ATTEMPTS+=1\r\n" +
		"if %ATTEMPTS% GEQ 60 goto fail\r\n" +
		"ping 127.0.0.1 -n 2 >NUL\r\n" +
		"goto delete\r\n" +
		":removed\r\n" +
		"if not \"%PURGE%\"==\"\" (start \"\" /B cmd /C \"ping 127.0.0.1 -n 2 ^>NUL ^& rmdir /S /Q ^\"%PURGE%^\"\" & exit /B 0)\r\n" +
		":cleanup\r\n" +
		"del /F /Q \"%BACKUP%\" >NUL 2>&1\r\n" +
		"del /F /Q \"%~f0\"\r\n" +
		"exit /B 0\r\n" +
		":fail\r\n" +
		"del /F /Q \"%~f0\"\r\n" +
		"exit /B 1\r\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		return "", fmt.Errorf("%w: 写入 Windows helper: %v", ErrState, err)
	}
	return helper, nil
}

// cmdValue is emitted into a batch file, so quote characters alone are not
// enough: cmd also parses metacharacters and expands percent variables.
func cmdValue(value string) string {
	replacer := strings.NewReplacer(
		"^", "^^",
		"&", "^&",
		"|", "^|",
		"<", "^<",
		">", "^>",
		"%", "%%",
	)
	return replacer.Replace(value)
}

func configureDetached(command any) error {
	cmd, ok := command.(*exec.Cmd)
	if !ok {
		return fmt.Errorf("%w: Windows helper 命令无效", ErrState)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008}
	return nil
}

func helperCommand(path string) *exec.Cmd { return exec.Command("cmd.exe", "/C", path) }

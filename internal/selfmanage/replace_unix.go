//go:build !windows

package selfmanage

import (
	"fmt"
	"os"
	"os/exec"
)

func replaceExecutable(target, staged string) error {
	if err := os.Rename(staged, target); err != nil {
		return fmt.Errorf("%w: 替换 jkv 二进制: %v", ErrState, err)
	}
	return nil
}

func writeWindowsHelper(string, int, string, string, bool, string) (string, error) {
	return "", fmt.Errorf("%w: Windows helper 在当前平台不可用", ErrState)
}

func configureDetached(any) error { return nil }

func helperCommand(path string) *exec.Cmd { return exec.Command(path) }

//go:build windows

package agent

import "os/exec"

func configureCommandCancel(cmd *exec.Cmd) {
	_ = cmd
}

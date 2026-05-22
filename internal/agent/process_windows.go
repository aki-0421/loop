//go:build windows

package agent

import (
	"os"
	"os/exec"
)

func configureCommandCancel(cmd *exec.Cmd) {
	cmd.WaitDelay = processCancelWaitDelay
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return cmd.Process.Kill()
	}
}

//go:build !windows

package cli

import (
	"context"
	"errors"
	"os"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func rendererInputSupported(renderer *runRenderer) bool {
	if renderer == nil || !renderer.enabled || !renderer.interactive {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func watchRendererInput(ctx context.Context, renderer *runRenderer) error {
	if renderer == nil {
		return errRendererInputUnavailable
	}
	fd := int(os.Stdin.Fd())
	restoreMode, err := makeNoncanonicalInputMode(fd)
	if err != nil {
		return err
	}
	defer restoreMode()

	restoreFlags, err := setNonblocking(fd)
	if err != nil {
		return err
	}
	defer restoreFlags()

	buf := make([]byte, 64)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ready, err := stdinReady(fd, sleepInputPollInterval)
		if err != nil {
			return err
		}
		if !ready {
			continue
		}
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				for _, b := range buf[:n] {
					renderer.handleInputByte(ctx, b)
				}
			}
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
				break
			}
			if err != nil {
				return err
			}
			if n == 0 {
				break
			}
		}
	}
}

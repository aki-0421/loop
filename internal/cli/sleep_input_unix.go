//go:build !windows

package cli

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func waitForSleepKeypress(ctx context.Context, d time.Duration) (bool, error) {
	if d <= 0 {
		return false, nil
	}
	fd := int(os.Stdin.Fd())
	restoreMode, err := makeNoncanonicalInputMode(fd)
	if err != nil {
		return false, err
	}
	defer restoreMode()

	restoreFlags, err := setNonblocking(fd)
	if err != nil {
		return false, err
	}
	defer restoreFlags()

	deadline := time.Now().Add(d)
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false, nil
		}
		wait := remaining
		if wait > sleepInputPollInterval {
			wait = sleepInputPollInterval
		}
		ready, err := stdinReady(fd, wait)
		if err != nil {
			return false, err
		}
		if !ready {
			continue
		}
		pressed, err := drainStdinInput()
		if pressed || err != nil {
			return pressed, err
		}
	}
}

func setNonblocking(fd int) (func(), error) {
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return func() {}, err
	}
	if flags&unix.O_NONBLOCK != 0 {
		return func() {}, nil
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags|unix.O_NONBLOCK); err != nil {
		return func() {}, err
	}
	return func() { _, _ = unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags) }, nil
}

func stdinReady(fd int, wait time.Duration) (bool, error) {
	timeoutMs := int(wait / time.Millisecond)
	if timeoutMs < 1 {
		timeoutMs = 1
	}
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, timeoutMs)
	if errors.Is(err, unix.EINTR) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return n > 0 && fds[0].Revents&unix.POLLIN != 0, nil
}

func drainStdinInput() (bool, error) {
	buf := make([]byte, 64)
	pressed := false
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			if containsInterruptByte(buf[:n]) {
				return false, context.Canceled
			}
			pressed = true
			continue
		}
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			return pressed, nil
		}
		if err != nil {
			return pressed, err
		}
		return pressed, nil
	}
}

func containsInterruptByte(buf []byte) bool {
	for _, b := range buf {
		if b == 0x03 {
			return true
		}
	}
	return false
}

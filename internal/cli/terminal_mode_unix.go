//go:build !windows

package cli

import "golang.org/x/sys/unix"

func makeNoncanonicalInputMode(fd int) (func(), error) {
	state, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return func() {}, err
	}
	next := *state
	next.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.IEXTEN
	next.Lflag |= unix.ISIG
	next.Cc[unix.VMIN] = 1
	next.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, &next); err != nil {
		return func() {}, err
	}
	return func() { _ = unix.IoctlSetTermios(fd, ioctlWriteTermios, state) }, nil
}

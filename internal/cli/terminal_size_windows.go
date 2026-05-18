//go:build windows

package cli

import (
	"os"
	"strconv"
)

func terminalSize() (int, int) {
	return terminalSizeFromEnv()
}

func terminalSizeFromEnv() (int, int) {
	width, _ := strconv.Atoi(os.Getenv("COLUMNS"))
	height, _ := strconv.Atoi(os.Getenv("LINES"))
	return width, height
}

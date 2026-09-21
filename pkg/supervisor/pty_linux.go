//go:build linux
// +build linux

package supervisor

import (
	"os"

	"github.com/pkg/term/termios"
)

func openPty() (master, slave *os.File, err error) {
	return termios.Pty()
}

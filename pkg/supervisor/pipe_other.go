//go:build !linux
// +build !linux

package supervisor

import (
	"os"
	"os/exec"
)

// newProcessPipe uses plain pipes on platforms that cannot allocate a
// controlling pty here (local dev/CI). Output streaming behaves the same
// as on Linux; signals are sent to the leader pid directly.
func newProcessPipe(cmd *exec.Cmd) (*processPipe, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	cmd.Stdout = writer
	cmd.Stderr = writer

	return &processPipe{reader: reader, writer: writer, signalGroup: false}, nil
}

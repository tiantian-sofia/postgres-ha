//go:build linux
// +build linux

package supervisor

import (
	"os/exec"
)

// newProcessPipe gives the command a controlling pty, matching the
// original behavior: the process becomes a session leader and stdout,
// stderr and stdin are all attached to the slave side.
func newProcessPipe(cmd *exec.Cmd) (*processPipe, error) {
	master, slave, err := openPty()
	if err != nil {
		return nil, err
	}

	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.Stdin = slave
	cmd.SysProcAttr.Setctty = true
	cmd.SysProcAttr.Setsid = true

	// With Setsid the command pid is also its process group id; signal
	// the group so children such as gosu/postgres receive it too.
	return &processPipe{reader: master, writer: slave, signalGroup: true}, nil
}

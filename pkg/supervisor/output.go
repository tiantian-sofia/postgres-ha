package supervisor

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/pkg/term/termios"
)

type ptyPipe struct {
	pty, tty *os.File
}

// newPipe opens the parent-side read file and the child-side file wired to
// the process stdio. Production uses a PTY; the indirection lets tests swap
// in plain pipes where the legacy termios dependency cannot open a PTY.
var newPipe = func() (parent, child *os.File, isTTY bool, err error) {
	pty, tty, err := termios.Pty()
	return pty, tty, true, err
}

type multiOutput struct {
	maxNameLength int
	mutex         sync.Mutex
	pipes         map[*process]*ptyPipe
}

func (m *multiOutput) openPipe(proc *process, cmd *exec.Cmd) (pipe *ptyPipe) {
	m.mutex.Lock()
	pipe = m.pipes[proc]
	m.mutex.Unlock()

	parent, child, isTTY, err := newPipe()
	fatalOnErr(err)

	pipe.pty, pipe.tty = parent, child
	cmd.Stdout = child
	cmd.Stderr = child
	cmd.Stdin = child
	cmd.SysProcAttr.Setctty = isTTY
	cmd.SysProcAttr.Setsid = true

	return pipe
}

func (m *multiOutput) Connect(proc *process) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if len(proc.name) > m.maxNameLength {
		m.maxNameLength = len(proc.name)
	}

	if m.pipes == nil {
		m.pipes = make(map[*process]*ptyPipe)
	}

	m.pipes[proc] = &ptyPipe{}
}

func (m *multiOutput) PipeOutput(proc *process, cmd *exec.Cmd) {
	pipe := m.openPipe(proc, cmd)

	go func(proc *process, pipe *ptyPipe) {
		scanner := bufio.NewScanner(pipe.pty)

		for scanner.Scan() {
			m.WriteLine(proc, scanner.Bytes())
		}
	}(proc, pipe)
}

func (m *multiOutput) ClosePipe(proc *process) {
	m.mutex.Lock()
	pipe := m.pipes[proc]
	m.mutex.Unlock()

	if pipe != nil {
		pipe.pty.Close()
		pipe.tty.Close()
	}
}

func (m *multiOutput) WriteLine(proc *process, p []byte) {
	var buf bytes.Buffer

	color := fmt.Sprintf("\033[1;38;5;%vm", proc.color)

	buf.WriteString(color)
	buf.WriteString(proc.name)

	for buf.Len()-len(color) < m.maxNameLength {
		buf.WriteByte(' ')
	}

	buf.WriteString("\033[0m | ")

	buf.Write(p)
	buf.WriteByte('\n')

	m.mutex.Lock()
	defer m.mutex.Unlock()

	buf.WriteTo(os.Stdout)
}

func (m *multiOutput) WriteErr(proc *process, err error) {
	m.WriteLine(proc, []byte(
		fmt.Sprintf("\033[0;31m%v\033[0m", err),
	))
}

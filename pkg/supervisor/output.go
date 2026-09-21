package supervisor

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

// processPipe connects a process's stdout/stderr to the reader goroutine
// that prefixes and forwards its output.
type processPipe struct {
	reader *os.File
	writer *os.File
	// signalGroup reports whether the command runs in its own process
	// group (pty mode on Linux), so signals should target -pid.
	signalGroup bool

	// readerDone is closed after the reader goroutine has drained the
	// pipe and exited.
	readerDone chan struct{}
}

type multiOutput struct {
	maxNameLength int
	mutex         sync.Mutex
	pipes         map[*process]*processPipe
}

func (m *multiOutput) pipe(proc *process) *processPipe {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.pipes[proc]
}

// openPipe allocates the output pipe (a pty on Linux) for a fresh process
// run and wires the command's std streams to it.
func (m *multiOutput) openPipe(proc *process, cmd *exec.Cmd) *processPipe {
	pipe, err := newProcessPipe(cmd)
	fatalOnErr(err)
	pipe.readerDone = make(chan struct{})
	proc.signalGroup = pipe.signalGroup

	m.mutex.Lock()
	m.pipes[proc] = pipe
	m.mutex.Unlock()

	return pipe
}

func (m *multiOutput) Connect(proc *process) {
	if len(proc.name) > m.maxNameLength {
		m.maxNameLength = len(proc.name)
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.pipes == nil {
		m.pipes = make(map[*process]*processPipe)
	}

	m.pipes[proc] = &processPipe{}
}

func (m *multiOutput) PipeOutput(proc *process, cmd *exec.Cmd) {
	pipe := m.openPipe(proc, cmd)

	go func() {
		defer close(pipe.readerDone)
		readPipeOutput(proc, pipe, m)
	}()
}

// readPipeOutput streams process output line by line. Unlike
// bufio.Scanner there is no fixed maximum line size, so long log lines
// (e.g. postgres recovery output) cannot wedge the logging pipeline.
func readPipeOutput(proc *process, pipe *processPipe, m *multiOutput) {
	reader := bufio.NewReader(pipe.reader)

	for {
		line, err := reader.ReadBytes('\n')

		if len(line) > 0 {
			for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
				line = line[:len(line)-1]
			}
			if len(line) > 0 {
				m.WriteLine(proc, line)
			}
		}

		if err != nil {
			// Reading a pty only fails with EOF/EIO once the session
			// side is gone; original scanner-based behavior ignored it.
			return
		}
	}
}

// ClosePipe closes the process side of the pty, lets the reader drain
// any buffered output and then closes the reader side. Waiting for the
// reader here guarantees all output is emitted before Run returns and
// serialises pipe teardown against the next process (re)start.
func (m *multiOutput) ClosePipe(proc *process) {
	pipe := m.pipe(proc)
	if pipe == nil || pipe.writer == nil {
		return
	}

	// Close the process side first so the reader can drain the rest and
	// observe EOF.
	pipe.writer.Close()

	if pipe.reader != nil && pipe.readerDone != nil {
		// A child of the command may have inherited the pipe and keep it
		// open; bound the drain so a stale writer cannot stall the
		// shutdown forever.
		select {
		case <-pipe.readerDone:
		case <-time.After(2 * time.Second):
		}
		pipe.reader.Close()
		<-pipe.readerDone
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

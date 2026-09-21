package supervisor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type cmdFactory func() *exec.Cmd

type process struct {
	name         string
	color        int
	output       *multiOutput
	stopSignal   os.Signal
	restart      bool
	restartDelay time.Duration
	maxRestarts  int

	f   cmdFactory
	dir string
	env []string

	// mu guards running, proc and exited. The exec.Cmd internals are
	// mutated by Start/Wait, so they must never be read from another
	// goroutine: signal targets are captured from cmd.Process under the
	// lock once Start has returned.
	mu      sync.Mutex
	proc    *os.Process
	running bool
	exited  chan struct{}
}

type Opt func(*process)

func WithEnv(env map[string]string) Opt {
	return func(proc *process) {
		for k, v := range env {
			proc.env = append(proc.env, fmt.Sprintf("%s=%s", k, v))
		}
	}
}

func WithStopSignal(sig os.Signal) Opt {
	return func(proc *process) {
		proc.stopSignal = sig
	}
}

func WithRootDir(dir string) Opt {
	return func(proc *process) {
		proc.dir = dir
	}
}

// WithRestart restarts the process if it exists. If limit
// is 0 it will restart forever.
func WithRestart(limit int, delay time.Duration) Opt {
	return func(proc *process) {
		proc.restart = true
		proc.maxRestarts = limit
		proc.restartDelay = delay
	}
}

func (p *process) writeLine(b []byte) {
	p.output.WriteLine(p, b)
}

func (p *process) writeErr(err error) {
	p.output.WriteErr(p, err)
}

// signal sends sig to the whole process group so children of the process
// (and the process itself) are notified. A process that exited between the
// running check and the signal is not an error worth reporting.
func signalGroup(proc *os.Process, sig os.Signal) error {
	group, err := os.FindProcess(-proc.Pid)
	if err != nil {
		return err
	}
	if err := group.Signal(sig); err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.EPERM) {
			return nil
		}
		return err
	}
	return nil
}

func (p *process) isRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

// signalTarget returns the process group to signal while the process runs.
func (p *process) signalTarget() *os.Process {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.running {
		return nil
	}
	return p.proc
}

func (p *process) Run() {
	cmd := p.f()

	p.mu.Lock()
	p.proc = nil
	p.running = false
	p.exited = make(chan struct{})
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.proc = nil
		p.running = false
		close(p.exited)
		p.mu.Unlock()
	}()

	p.output.PipeOutput(p, cmd)
	defer p.output.ClosePipe(p)

	ensureKill(cmd)

	p.writeLine([]byte("\033[1mRunning...\033[0m"))

	if err := cmd.Start(); err != nil {
		p.writeErr(err)
		return
	}

	p.mu.Lock()
	p.proc = cmd.Process
	p.running = true
	p.mu.Unlock()

	if err := cmd.Wait(); err != nil {
		p.writeErr(err)
	} else {
		status := cmd.ProcessState.ExitCode()
		p.writeLine([]byte(fmt.Sprintf("\033[1mProcess exited %d\033[0m", status)))
	}
}

// wait blocks until the current invocation of Run has returned or deadline,
// whichever comes first.
func (p *process) wait(deadline time.Time) {
	p.mu.Lock()
	exited := p.exited
	p.mu.Unlock()

	if exited == nil {
		return
	}

	if d := time.Until(deadline); d > 0 {
		select {
		case <-exited:
		case <-time.After(d):
		}
	} else {
		<-exited
	}
}

func (p *process) Interrupt() {
	target := p.signalTarget()
	if target == nil {
		return
	}

	p.writeLine([]byte(fmt.Sprintf("\033[1mStopping %s...\033[0m", p.stopSignal)))
	if err := signalGroup(target, p.stopSignal); err != nil {
		p.writeErr(err)
	}
}

func (p *process) Kill() {
	target := p.signalTarget()
	if target == nil {
		return
	}

	p.writeLine([]byte("\033[1mKilling...\033[0m"))
	if err := signalGroup(target, syscall.SIGKILL); err != nil {
		p.writeErr(err)
	}
}

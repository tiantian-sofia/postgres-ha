package supervisor

import (
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

	mu          sync.Mutex
	cmd         *exec.Cmd
	started     bool
	signalGroup bool

	// interruptRequested and killRequested latch the shutdown request so
	// a process that is (re)started while the supervisor is stopping
	// receives the signal right after launch, and repeated requests do
	// not result in repeated signals.
	interruptRequested bool
	killRequested      bool

	// stop is the shutdown channel for the Run iteration currently
	// executing; it is nil outside Run.
	stop <-chan struct{}
	// done is closed when the current cmd (if any) has been reaped.
	done chan struct{}
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

// signalLocked sends sig to the process group of the current command.
// Callers must hold p.mu.
func (p *process) signalLocked(sig os.Signal) {
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}

	target := p.cmd.Process
	if p.signalGroup {
		// With a controlling pty the command is a session leader, so its
		// process group id equals its pid. Signalling the group makes
		// sure children such as gosu/postgres receive it too.
		var err error
		target, err = os.FindProcess(-p.cmd.Process.Pid)
		if err != nil {
			p.writeErr(err)
			return
		}
	}

	if err := target.Signal(sig); err != nil {
		p.writeErr(err)
	}
}

func (p *process) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Tracked on p rather than reading cmd.ProcessState, which Wait
	// mutates without holding this mutex.
	return p.started
}

// exited returns a channel closed once the current command has been
// reaped.
func (p *process) exited() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done
}

func (p *process) Run(stop <-chan struct{}) {
	cmd := p.f()

	done := make(chan struct{})

	p.mu.Lock()
	p.cmd = cmd
	p.started = false
	p.interruptRequested = false
	p.killRequested = false
	p.done = done
	p.stop = stop
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.cmd = nil
		p.started = false
		p.stop = nil
		close(done)
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
	p.started = true
	// Shutdown could have started before (or while) the command was
	// launched, in which case Interrupt ran before started became true
	// and only latched. Reconcile the current state now that the process
	// exists, so it cannot miss the signal.
	select {
	case <-p.stop:
		// Shutdown began before the process was started: deliver the
		// graceful signal now unless a kill was requested.
		if !p.killRequested && !p.interruptRequested {
			p.interruptRequested = true
			p.signalLocked(p.stopSignal)
		}
	default:
	}
	if p.killRequested {
		p.signalLocked(syscall.SIGKILL)
	} else if p.interruptRequested {
		p.signalLocked(p.stopSignal)
	}
	p.mu.Unlock()

	if err := cmd.Wait(); err != nil {
		p.writeErr(err)
	} else {
		status := cmd.ProcessState.ExitCode()
		p.writeLine([]byte(fmt.Sprintf("\033[1mProcess exited %d\033[0m", status)))
	}
}

func (p *process) Interrupt() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.interruptRequested {
		return
	}
	p.interruptRequested = true

	if p.started {
		p.writeLine([]byte(fmt.Sprintf("\033[1mStopping %s...\033[0m", p.stopSignal)))
		p.signalLocked(p.stopSignal)
	}
}

func (p *process) Kill() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.killRequested {
		return
	}
	p.killRequested = true

	if p.started {
		p.writeLine([]byte("\033[1mKilling...\033[0m"))
		p.signalLocked(syscall.SIGKILL)
	}
}

package supervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/fly-examples/postgres-ha/pkg/server"
	"github.com/google/shlex"
	"golang.org/x/sync/errgroup"
)

type processError struct {
	process *process
}

func (pe *processError) Error() string {
	return fmt.Sprintf("process %s failed", pe.process.name)
}

type Supervisor struct {
	name    string
	output  *multiOutput
	procs   []*process
	stop    chan struct{}
	timeout time.Duration

	stopOnce     sync.Once
	shutdownOnce sync.Once
}

func New(name string, timeout time.Duration) *Supervisor {
	return &Supervisor{
		timeout: timeout,
		name:    name,
		output:  &multiOutput{},
		stop:    make(chan struct{}),
	}
}

var colors = []int{2, 3, 4, 5, 6, 42, 130, 103, 129, 108}

func (h *Supervisor) AddProcess(name string, command string, opts ...Opt) {
	proc := &process{
		name:       name,
		color:      colors[len(h.procs)%len(colors)],
		output:     h.output,
		stopSignal: syscall.SIGINT,
		env:        os.Environ(),
	}

	parsedCmd, err := shlex.Split(command)
	fatalOnErr(err)

	proc.f = func() *exec.Cmd {
		cmd := exec.Command(parsedCmd[0], parsedCmd[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{}
		cmd.Env = proc.env
		cmd.Dir = proc.dir

		return cmd
	}

	for _, opt := range opts {
		opt(proc)
	}

	proc.output.Connect(proc)

	h.procs = append(h.procs, proc)
}

func (h *Supervisor) runProcess(ctx context.Context, proc *process) error {
	restarts := 0

	for {
		proc.Run()

		// supervisor is stopping, exit
		if ctx.Err() != nil {
			return nil
		}

		// process is done, exit
		if !proc.restart {
			proc.writeLine([]byte("done"))
			return nil
		}

		// process restart limit reached, crash supervisor
		if proc.maxRestarts > 0 && restarts >= proc.maxRestarts {
			proc.writeLine([]byte("restart attempts exhausted, crashing"))
			return &processError{proc}
		}

		restarts++
		proc.writeLine([]byte(fmt.Sprintf("restarting in %s [attempt %d]", proc.restartDelay, restarts)))
		select {
		case <-time.After(proc.restartDelay):
		case <-ctx.Done():
			return nil
		}
	}
}

// crashGrace is the teardown window when the supervisor dies on its own
// (a process exhausted its restart budget): sibling processes get a brief
// chance to leave cleanly before being killed.
var crashGrace = 5 * time.Second

// stopProcesses performs the full teardown: initial stopSignal, wait out the
// grace period, then escalate survivors to SIGKILL and reap them before Run
// is allowed to return. It runs exactly once, so repeated SIGTERMs cannot
// restart it and reset the remaining grace period.
func (h *Supervisor) stopProcesses(grace time.Duration) {
	h.shutdownOnce.Do(func() {
		fmt.Println("supervisor stopping")
		for _, proc := range h.procs {
			proc.Interrupt()
		}
	})

	waiting := make([]*process, 0, len(h.procs))
	for _, proc := range h.procs {
		if proc.isRunning() {
			waiting = append(waiting, proc)
		}
	}

	if grace > 0 {
		deadline := time.Now().Add(grace)
		for _, proc := range waiting {
			proc.wait(deadline)
		}
	}

	killing := make([]*process, 0, len(h.procs))
	for _, proc := range h.procs {
		if proc.isRunning() {
			killing = append(killing, proc)
		}
	}
	for _, proc := range killing {
		proc.Kill()
	}

	// SIGKILL is untrappable, but give reap a bounded moment before returning
	// so Run() cannot leave zombies behind for Pdeathsig to clean up.
	killDeadline := time.Now().Add(5 * time.Second)
	for _, proc := range killing {
		proc.wait(killDeadline)
	}
}

func (h *Supervisor) StartHttpListener() {
	go server.StartHttpServer()
}

func (h *Supervisor) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		select {
		case <-h.stop:
			cancel()
		case <-ctx.Done():
		}
	}()

	eg, egCtx := errgroup.WithContext(ctx)

	for _, proc := range h.procs {
		p := proc
		eg.Go(func() error {
			return h.runProcess(egCtx, p)
		})
	}

	// Teardown must progress concurrently with eg.Wait(): processes that
	// ignore their stopSignal are only reaped by the SIGKILL escalation, and
	// eg.Wait cannot return while they are still alive. An explicit Stop gets
	// the configured grace period; a self-initiated shutdown (a process gave
	// up restarting) stays fast so the crash is not delayed.
	shutdownDone := make(chan struct{})
	go func() {
		grace := crashGrace
		select {
		case <-h.stop:
			grace = h.timeout
		case <-egCtx.Done():
		}
		h.stopProcesses(grace)
		close(shutdownDone)
	}()

	err := eg.Wait()
	<-shutdownDone

	return err
}

// Stop signals the supervisor to begin shutting down. It is idempotent and
// never blocks: repeated SIGTERMs arrive on an already-closed channel and
// cannot reset the remaining grace period.
func (h *Supervisor) Stop() {
	h.stopOnce.Do(func() {
		close(h.stop)
	})
}

func (h *Supervisor) StopOnSignal(sigs ...os.Signal) {
	sigch := make(chan os.Signal, 1)
	signal.Notify(sigch, sigs...)

	go func() {
		for sig := range sigch {
			fmt.Printf("Got %s, stopping\n", sig)
			h.Stop()
		}
	}()
}

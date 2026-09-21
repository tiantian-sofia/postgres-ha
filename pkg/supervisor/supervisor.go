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

// lifecycle is the state of one Run generation. All three channels are
// created together and only ever closed, never sent to:
//   - started is closed when Run enters this generation,
//   - stop is closed when shutdown is requested for this generation,
//   - done is closed when Run has finished the whole shutdown sequence.
//
// A Stop requested before any Run closes stop on generation 0; Run
// adopts that already-closed stop channel when it starts.
type lifecycle struct {
	started chan struct{}
	stop    chan struct{}
	done    chan struct{}
}

func newLifecycle() *lifecycle {
	return &lifecycle{
		started: make(chan struct{}),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (lc *lifecycle) stopping() bool {
	select {
	case <-lc.stop:
		return true
	default:
		return false
	}
}

type Supervisor struct {
	name    string
	output  *multiOutput
	procs   []*process
	timeout time.Duration

	lcMu sync.Mutex
	lc   *lifecycle
}

func New(name string, timeout time.Duration) *Supervisor {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	return &Supervisor{
		timeout: timeout,
		name:    name,
		output:  &multiOutput{},
		lc:      newLifecycle(),
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
		done:       make(chan struct{}),
	}
	close(proc.done)

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

func (h *Supervisor) runProcess(ctx context.Context, proc *process, stop <-chan struct{}) error {
	restarts := 0

	for {
		proc.Run(stop)

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

// waitForExit runs the shutdown sequence. It only returns once every
// process has been notified, the configured grace period has elapsed
// for processes that are still alive and every remaining process has
// been killed and reaped.
func (h *Supervisor) waitForExit(ctx context.Context) {
	<-ctx.Done()

	fmt.Println("supervisor stopping")

	for _, proc := range h.procs {
		proc.Interrupt()
	}

	grace := time.NewTimer(h.timeout)
	defer grace.Stop()

	for _, proc := range h.procs {
		if !proc.Running() {
			continue
		}
		select {
		case <-proc.exited():
		case <-grace.C:
		}
	}

	for _, proc := range h.procs {
		proc.Kill()
	}

	for _, proc := range h.procs {
		// After context cancellation runProcess never starts another
		// command, so this is the final done channel.
		<-proc.exited()
	}
}

func (h *Supervisor) StartHttpListener() {
	go server.StartHttpServer()
}

func (h *Supervisor) Run() error {
	h.lcMu.Lock()
	lc := h.lc
	if isClosed(lc.started) {
		// A previous Run published this generation; start a fresh one
		// while preserving a Stop already queued for it.
		next := newLifecycle()
		if lc.stopping() {
			close(next.stop)
		}
		lc = next
		h.lc = lc
	}
	close(lc.started)
	h.lcMu.Unlock()

	stopped := lc.stopping()

	defer func() {
		h.lcMu.Lock()
		// Publish the next generation. Carry a pending stop over only
		// when this Run actually observed one; a Run that finished on its
		// own leaves the next generation pristine even if a Stop lands
		// during teardown.
		next := newLifecycle()
		if stopped {
			close(next.stop)
		}
		h.lc = next
		h.lcMu.Unlock()

		close(lc.done)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if lc.stopping() {
		cancel()
	} else {
		go func() {
			select {
			case <-lc.stop:
				cancel()
			case <-ctx.Done():
			}
		}()
	}

	eg, egCtx := errgroup.WithContext(ctx)

	for _, proc := range h.procs {
		p := proc
		eg.Go(func() error {
			return h.runProcess(egCtx, p, lc.stop)
		})
	}

	shutdownDone := make(chan struct{})
	go func() {
		h.waitForExit(egCtx)
		close(shutdownDone)
	}()

	err := eg.Wait()

	// Do not return until interrupt/kill and reaping have finished.
	<-shutdownDone

	return err
}

// Stop requests a graceful shutdown. Repeated calls, including repeated
// signals during the grace period, are coalesced and do not extend or
// reset the remaining wait time. When Run is active, Stop blocks until
// the full shutdown sequence (interrupt, grace period, kill and reap)
// has completed; a Stop requested before Run starts is delivered as
// soon as Run begins.
func (h *Supervisor) Stop() {
	h.lcMu.Lock()
	lc := h.lc
	active := isClosed(lc.started) && !isClosed(lc.done)
	if !lc.stopping() {
		close(lc.stop)
	}
	h.lcMu.Unlock()

	if active {
		<-lc.done
	}
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
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

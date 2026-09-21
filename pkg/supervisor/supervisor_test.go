package supervisor

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// End-to-end signal delivery: an actual SIGTERM sent to the process must
// reach StopOnSignal and result in a graceful shutdown honouring timeout.
func TestSIGTERMEndToEnd(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	stopped := filepath.Join(dir, "stopped")

	script := `trap "echo ok > ` + stopped + `; exit 0" TERM; echo ok > ` + ready + `; sleep 30`

	h := New("test", 10*time.Second)
	h.AddProcess("app", "sh -c '"+script+"'", WithStopSignal(syscall.SIGTERM))
	h.StopOnSignal(syscall.SIGTERM)

	done := runAsync(h)
	waitForFile(t, ready, 5*time.Second)

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after SIGTERM")
	}
	if _, err := os.Stat(stopped); err != nil {
		t.Fatalf("child did not shut down on SIGTERM: %v", err)
	}
}

func TestMain(m *testing.M) {
	// The legacy termios PTY backend cannot run on newer macOS, so exercise
	// the same wiring through plain pipes.
	newPipe = func() (parent, child *os.File, isTTY bool, err error) {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, nil, false, err
		}
		return r, w, false, nil
	}
	os.Exit(m.Run())
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for file %s", path)
}

func runAsync(h *Supervisor) chan error {
	done := make(chan error, 1)
	go func() {
		done <- h.Run()
	}()
	return done
}

// The configured timeout is the grace period: a process that handles the
// configured stopSignal and exits quickly must be reaped well before the
// (much larger) timeout instead of being killed after a hardcoded delay.
func TestGracefulStopHonorsStopSignalAndTimeout(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	stopped := filepath.Join(dir, "stopped")

	script := `trap "echo ok > ` + stopped + `; exit 0" TERM; echo ok > ` + ready + `; sleep 30`

	h := New("test", 10*time.Second)
	h.AddProcess("app", "sh -c '"+script+"'", WithStopSignal(syscall.SIGTERM))

	done := runAsync(h)
	waitForFile(t, ready, 5*time.Second)

	start := time.Now()
	h.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Run did not return after graceful stop")
	}
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Fatalf("graceful stop took %s, expected well under the 10s timeout", elapsed)
	}
	if _, err := os.Stat(stopped); err != nil {
		t.Fatalf("process did not receive SIGTERM gracefully: %v", err)
	}
}

// A process that ignores the stopSignal must be left alone for the full grace
// period and then escalated to SIGKILL, and Run must reap it.
func TestTimeoutEscalatesToKill(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")

	script := `trap "" TERM; echo ok > ` + ready + `; sleep 30`

	h := New("test", 500*time.Millisecond)
	h.AddProcess("app", "sh -c '"+script+"'", WithStopSignal(syscall.SIGTERM))

	done := runAsync(h)
	waitForFile(t, ready, 5*time.Second)

	start := time.Now()
	h.Stop()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after kill escalation")
	}
	elapsed := time.Since(start)

	if elapsed < 300*time.Millisecond {
		t.Fatalf("escalated to SIGKILL after %s, expected the full 500ms grace", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("kill escalation took %s, process was not reaped promptly", elapsed)
	}
}

// Repeated stop notifications must not reset the remaining grace period: the
// process keeps its whole timeout to shut down and must not be SIGKILLed just
// because SIGTERM arrived more than once.
func TestRepeatedStopDoesNotResetGrace(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	clean := filepath.Join(dir, "clean")

	script := `trap "sleep 1; echo ok > ` + clean + `; exit 0" TERM; echo ok > ` + ready + `; sleep 30`

	h := New("test", 5*time.Second)
	h.AddProcess("app", "sh -c '"+script+"'", WithStopSignal(syscall.SIGTERM))

	done := runAsync(h)
	waitForFile(t, ready, 5*time.Second)

	start := time.Now()
	h.Stop()
	go func() {
		for i := 0; i < 30; i++ {
			h.Stop()
			time.Sleep(50 * time.Millisecond)
		}
	}()

	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("Run did not return")
	}
	elapsed := time.Since(start)

	if elapsed < 800*time.Millisecond {
		t.Fatalf("process was killed after %s, repeated stop reset the grace period", elapsed)
	}
	if _, err := os.Stat(clean); err != nil {
		t.Fatal("process was SIGKILLed instead of finishing its graceful shutdown")
	}
}

// Regression for the restore path: svisor.Run() runs in a goroutine and Stop()
// is called once the child has already exited. Stop must never block on an
// unbuffered channel.
func TestStopAfterRunReturnedDoesNotBlock(t *testing.T) {
	h := New("test", time.Second)
	h.AddProcess("app", "sh -c 'exit 0'")

	done := runAsync(h)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after process exited")
	}

	stopped := make(chan struct{})
	go func() {
		h.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked after Run had already returned")
	}
}

// Hammer cmd from both sides while a restarting process cycles through Run:
// Interrupt/Kill/isRunning must not race the deferred cmd reset.
func TestProcessCmdFieldNoRace(t *testing.T) {
	h := New("test", time.Second)
	h.AddProcess("app", "sh -c 'exit 0'", WithRestart(0, 10*time.Millisecond))

	done := runAsync(h)

	proc := h.procs[0]
	stopHammer := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopHammer:
				return
			default:
				proc.Interrupt()
				proc.isRunning()
			}
		}
	}()

	time.Sleep(300 * time.Millisecond)
	h.Stop()
	close(stopHammer)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}

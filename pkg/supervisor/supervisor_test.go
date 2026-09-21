package supervisor

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// writeScript writes a small /bin/sh script into the test's temp dir and
// returns a supervisor command string that executes it.
func writeScript(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "run.sh")
	content := "#!/bin/sh\nset -e\n" + body + "\n"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	return "/bin/sh " + strconv.Quote(path)
}

func waitForRunning(t *testing.T, s *Supervisor, name string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range s.procs {
			if p.name == name && p.Running() {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %q never became running", name)
}

func waitForFile(t *testing.T, path string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file %q never appeared", path)
}

// The stop signal (default SIGINT) must be delivered first and a process
// that exits on it must be left alone for the whole grace period.
func TestGracefulShutdownUsesStopSignal(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "stopped")
	ready := filepath.Join(dir, "ready")

	cmd := writeScript(t, `
trap 'touch '`+strconv.Quote(marker)+`'; exit 0' INT
touch `+strconv.Quote(ready)+`
while :; do sleep 1; done
`)

	s := New("test", 3*time.Second)
	s.AddProcess("app", cmd)

	go func() {
		waitForFile(t, ready)
		s.Stop()
	}()

	start := time.Now()
	if err := s.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("graceful shutdown took %s, should not wait for the full timeout", elapsed)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("stop signal trap did not run: %v", err)
	}
}

// WithStopSignal controls which signal is used for graceful shutdown.
func TestWithStopSignal(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "stopped")
	ready := filepath.Join(dir, "ready")

	cmd := writeScript(t, `
trap 'touch '`+strconv.Quote(marker)+`'; exit 0' TERM
touch `+strconv.Quote(ready)+`
while :; do sleep 1; done
`)

	s := New("test", 2*time.Second)
	s.AddProcess("app", cmd, WithStopSignal(syscall.SIGTERM))

	go func() {
		waitForFile(t, ready)
		s.Stop()
	}()

	if err := s.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("SIGTERM trap did not run: %v", err)
	}
}

// A process ignoring the graceful signal must be SIGKILLed only once the
// configured timeout has actually elapsed.
func TestKillEscalatesAfterTimeout(t *testing.T) {
	// Build a tiny helper that ignores SIGTERM deterministically (no
	// shell fork/exec race), writes a readiness file and sleeps until
	// killed.
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	bin := buildIgnoreSignalsHelper(t, dir)

	timeout := 400 * time.Millisecond
	s := New("test", timeout)
	s.AddProcess("app", bin+" "+strconv.Quote(ready)+" 30", WithStopSignal(syscall.SIGTERM))

	go func() {
		waitForFile(t, ready)
		s.Stop()
	}()

	start := time.Now()
	if err := s.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed < timeout-100*time.Millisecond {
		t.Fatalf("process was killed after %s, before the %s grace period", elapsed, timeout)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("shutdown took %s, kill escalation did not happen", elapsed)
	}
}

// Repeated Stop calls (i.e. repeated SIGTERMs) must be coalesced and must
// not shorten or reset the remaining grace period.
func TestRepeatedStopDoesNotResetGrace(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	bin := buildIgnoreSignalsHelper(t, dir)

	timeout := 500 * time.Millisecond
	s := New("test", timeout)
	s.AddProcess("app", bin+" "+strconv.Quote(ready)+" 30", WithStopSignal(syscall.SIGTERM))

	go func() {
		waitForFile(t, ready)
		time.Sleep(100 * time.Millisecond)
		s.Stop()
	}()
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.Stop()
	}()
	go func() {
		time.Sleep(300 * time.Millisecond)
		s.Stop()
	}()

	start := time.Now()
	if err := s.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed < timeout-150*time.Millisecond {
		t.Fatalf("repeated Stop cut the grace period short: %s", elapsed)
	}
}

// Repeated real SIGTERMs must behave the same as repeated Stop calls.
func TestRepeatedSIGTERM(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	bin := buildIgnoreSignalsHelper(t, dir)

	timeout := 500 * time.Millisecond
	s := New("test", timeout)
	s.AddProcess("app", bin+" "+strconv.Quote(ready)+" 30", WithStopSignal(syscall.SIGTERM))
	s.StopOnSignal(syscall.SIGTERM)

	started := make(chan struct{})
	go func() {
		<-started
		waitForFile(t, ready)
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		time.Sleep(150 * time.Millisecond)
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		time.Sleep(150 * time.Millisecond)
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	}()

	start := time.Now()
	close(started)
	if err := s.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed < timeout-150*time.Millisecond {
		t.Fatalf("repeated SIGTERM cut the grace period short: %s", elapsed)
	}
}

// Run must not return until the shutdown sequence is completely finished,
// including slow cleanup performed by the child.
func TestRunWaitsForChildCleanup(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "cleanup-done")
	ready := filepath.Join(dir, "ready")

	cmd := writeScript(t, `
trap 'sleep 0.4; touch '`+strconv.Quote(marker)+`'; exit 0' INT
touch `+strconv.Quote(ready)+`
while :; do sleep 1; done
`)

	s := New("test", 3*time.Second)
	s.AddProcess("app", cmd)

	go func() {
		waitForFile(t, ready)
		s.Stop()
	}()

	start := time.Now()
	if err := s.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	elapsed := time.Since(start)

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("Run returned before child cleanup finished: %v", err)
	}
	if elapsed < 300*time.Millisecond {
		t.Fatalf("Run returned in %s, child cleanup was not awaited", elapsed)
	}
}

// Stop requested before (or while) Run starts must still shut everything
// down promptly instead of hanging.
func TestStopBeforeRun(t *testing.T) {
	cmd := writeScript(t, `exec sleep 30`)

	s := New("test", time.Second)
	s.AddProcess("app", cmd)

	go s.Stop()

	done := make(chan error, 1)
	go func() { done <- s.Run() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run hung when Stop was requested before startup")
	}
}

// Rapid restart cycles must be race free: the cmd field and the output
// pipes are accessed concurrently by Run and Interrupt/Kill.
func TestRestartCycleRace(t *testing.T) {
	cmd := writeScript(t, `exit 0`)

	s := New("test", 2*time.Second)
	s.AddProcess("app", cmd, WithRestart(0, time.Millisecond))

	go func() {
		waitForRunning(t, s, "app")
		deadline := time.Now().Add(400 * time.Millisecond)
		for time.Now().Before(deadline) {
			for _, p := range s.procs {
				p.Interrupt()
				p.Kill()
			}
			time.Sleep(2 * time.Millisecond)
		}
		s.Stop()
	}()

	if err := s.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// Output longer than the old bufio.Scanner token limit (64KiB) must not
// wedge the logging pipeline: lines emitted afterwards still come through.
func TestLongLinesKeepFlowing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty based output is unix only")
	}

	cmd := writeScript(t, `
awk 'BEGIN { i=0; while (i++ < 200000) printf "x"; print ""; print "AFTERLONG-MARKER"; fflush() }'
sleep 30
`)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = w

	var collected bytes.Buffer
	var collectedMu sync.Mutex
	collectDone := make(chan struct{})
	defer func() {
		// Restore only after the collector has drained everything;
		// swapping os.Stdout earlier races the supervisor's last writes.
		<-collectDone
		os.Stdout = oldStdout
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			collectedMu.Lock()
			collected.Write(buf[:n])
			collectedMu.Unlock()
			if err != nil {
				break
			}
		}
		close(collectDone)
	}()
	hasMarker := func() bool {
		collectedMu.Lock()
		defer collectedMu.Unlock()
		return strings.Contains(collected.String(), "AFTERLONG-MARKER")
	}

	s := New("test", 2*time.Second)
	s.AddProcess("app", cmd)

	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		waitForRunning(t, s, "app")
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if hasMarker() {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		s.Stop()
	}()

	if err := s.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Join the stopper goroutine before Run's state can be touched again.
	<-stopDone

	_ = w.Close()
	<-collectDone

	collectedMu.Lock()
	gotMarker := strings.Contains(collected.String(), "AFTERLONG-MARKER")
	collectedMu.Unlock()
	if !gotMarker {
		t.Fatalf("output after a long line was lost (logging pipeline wedged)")
	}
}

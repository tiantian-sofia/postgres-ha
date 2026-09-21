// Command ignoresignals is a test helper: it handles (and ignores)
// SIGTERM/SIGINT, writes a readiness marker, then sleeps. It lets
// shutdown tests verify SIGKILL escalation without depending on shell
// signal semantics.
package main

import (
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 3 {
		os.Exit(2)
	}
	readyFile := os.Args[1]
	seconds, err := strconv.Atoi(os.Args[2])
	if err != nil {
		os.Exit(2)
	}

	// Register an explicit handler that swallows the signals. A channel
	// notification is committed synchronously by signal.Notify.
	swallow := make(chan os.Signal, 4)
	signal.Notify(swallow, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		for range swallow {
		}
	}()

	if err := os.WriteFile(readyFile, []byte("ready"), 0o644); err != nil {
		os.Exit(2)
	}

	time.Sleep(time.Duration(seconds) * time.Second)
}

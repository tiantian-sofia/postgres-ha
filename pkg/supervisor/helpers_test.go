package supervisor

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// buildIgnoreSignalsHelper compiles the ignore-signals test helper into
// dir and returns its executable path. The helper deterministically
// ignores SIGTERM/SIGINT, avoiding shell fork/exec signal races.
func buildIgnoreSignalsHelper(t *testing.T, dir string) string {
	t.Helper()

	bin := filepath.Join(dir, "ignoresignals")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	source := filepath.Join("testdata", "ignoresignals", "main.go")
	cmd := exec.Command("go", "build", "-o", bin, source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v\n%s", err, out)
	}
	return bin
}

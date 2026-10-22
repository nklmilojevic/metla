package busy

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseLsof(t *testing.T) {
	out := []byte("p10\ncfish\nfcwd\nn/w/app\np11\ncclaude\nfcwd\nn/w/app/sub\np12\ncnode\nfcwd\nn/w/app-other\np13\ncgo\nfcwd\nn/w/app\n")
	tab := build(parseLsof(out), map[int]bool{13: true})
	got := tab.In("/w/app")
	if len(got) != 1 || got[0].PID != 11 || got[0].Command != "claude" {
		t.Errorf("In(/w/app) = %+v, want only claude: shells and metla itself do not count, siblings do not match", got)
	}
}

// A real process sitting in a directory shows up in a real snapshot.
func TestSnapshot(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sleep", "30")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if _, err := exec.LookPath("lsof"); err != nil && !exists("/proc") {
		t.Skip("neither lsof nor /proc")
	}
	for _, p := range Snapshot().In(dir) {
		if p.PID == cmd.Process.Pid {
			return
		}
	}
	t.Errorf("sleep (pid %d) not found in %s", cmd.Process.Pid, dir)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

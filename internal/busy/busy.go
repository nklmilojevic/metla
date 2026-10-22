// Package busy finds processes working inside a directory, so metla never
// removes a worktree an agent, dev server or watcher is running in.
package busy

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/nklmilojevic/metla/internal/scan"
)

// shells sitting idle in a worktree do not make it busy, or a terminal tab
// left open in a finished worktree would pin it forever.
var shells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true,
	"tcsh": true, "csh": true, "nu": true, "elvish": true, "xonsh": true, "pwsh": true,
	"login": true,
}

// Table maps working directories to the processes in them.
type Table struct {
	cwds map[string][]scan.Proc
}

// Snapshot reads the process table once. It returns an empty table when
// neither /proc nor lsof is available.
func Snapshot() *Table {
	var procs []proc
	if runtime.GOOS == "linux" {
		procs = fromProc()
	} else {
		procs = fromLsof()
	}
	return build(procs, ancestors(os.Getpid()))
}

type proc struct {
	pid     int
	command string
	cwd     string
}

func build(procs []proc, skip map[int]bool) *Table {
	t := &Table{cwds: map[string][]scan.Proc{}}
	for _, p := range procs {
		if skip[p.pid] || shells[filepath.Base(p.command)] || p.cwd == "" {
			continue
		}
		t.cwds[p.cwd] = append(t.cwds[p.cwd], scan.Proc{PID: p.pid, Command: p.command})
	}
	return t
}

// In returns the processes whose working directory is path or below it.
func (t *Table) In(path string) []scan.Proc {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		path = r
	}
	prefix := path + string(filepath.Separator)
	var out []scan.Proc
	for cwd, ps := range t.cwds {
		if cwd == path || strings.HasPrefix(cwd, prefix) {
			out = append(out, ps...)
		}
	}
	return out
}

// fromLsof reads every process's cwd with one lsof call. lsof -F prints
// one field per line: p<pid>, c<command>, then f<fd> and n<name>.
func fromLsof() []proc {
	out, err := exec.Command("lsof", "-w", "+c", "0", "-d", "cwd", "-F", "pcn").Output()
	if err != nil && len(out) == 0 {
		return nil
	}
	return parseLsof(out)
}

func parseLsof(out []byte) []proc {
	var procs []proc
	var cur proc
	flush := func() {
		if cur.pid != 0 && cur.cwd != "" {
			procs = append(procs, cur)
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			flush()
			pid, _ := strconv.Atoi(line[1:])
			cur = proc{pid: pid}
		case 'c':
			cur.command = line[1:]
		case 'n':
			cur.cwd = line[1:]
		}
	}
	flush()
	return procs
}

func fromProc() []proc {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var procs []proc
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		cwd, err := os.Readlink(filepath.Join("/proc", e.Name(), "cwd"))
		if err != nil {
			continue
		}
		comm, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
		procs = append(procs, proc{pid: pid, command: strings.TrimSpace(string(comm)), cwd: cwd})
	}
	return procs
}

// ancestors returns pid and its parents, so running metla from inside a
// worktree does not mark that worktree busy.
func ancestors(pid int) map[int]bool {
	seen := map[int]bool{}
	for pid > 1 && !seen[pid] {
		seen[pid] = true
		pid = parent(pid)
	}
	return seen
}

func parent(pid int) int {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	ppid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return ppid
}

package scan

import "fmt"

// classify sets the verdict and the reasons behind it. The first rule that
// applies wins: keep, then unsafe, then safe, then review.
func classify(w *Worktree) {
	w.Verdict, w.Reasons = verdict(w)
	if w.Locked && w.LockStale {
		w.Reasons = append(w.Reasons, "stale lock (its process is gone)")
	}
}

func verdict(w *Worktree) (Verdict, []string) {
	switch {
	case w.Primary:
		return Keep, []string{"main worktree"}
	case w.Protected:
		return Keep, []string{fmt.Sprintf("protected branch %s", w.Branch)}
	case w.Locked && !w.LockStale:
		if w.LockReason == "" {
			return Keep, []string{"locked"}
		}
		return Keep, []string{"locked: " + w.LockReason}
	}

	var risks []string
	if w.Orphan {
		risks = append(risks, "repository is gone")
	}
	for _, p := range w.Busy {
		risks = append(risks, fmt.Sprintf("in use by %s (pid %d)", p.Command, p.PID))
	}
	if w.Dirty {
		risks = append(risks, plural(w.DirtyFiles, "uncommitted change"))
	}
	if w.Unreachable > 0 {
		risks = append(risks, plural(w.Unreachable, "commit")+" only on this detached HEAD")
	}
	// Merged work is accounted for upstream, whatever the unpushed count says.
	if w.Unpushed > 0 && !w.Merged {
		risks = append(risks, plural(w.Unpushed, "unpushed commit"))
	}
	if len(risks) > 0 {
		return Unsafe, risks
	}

	switch {
	case w.Merged && w.MergedBy == "pr":
		return Safe, []string{fmt.Sprintf("PR #%d merged", w.PR.Number)}
	case w.Merged:
		return Safe, []string{mergedReason(w.MergedBy)}
	case w.PR != nil:
		return Review, []string{fmt.Sprintf("PR #%d %s", w.PR.Number, lower(w.PR.State))}
	case w.Detached:
		return Review, []string{"detached HEAD, commits held elsewhere"}
	}
	return Review, []string{"not merged"}
}

func mergedReason(how string) string {
	switch how {
	case "squash":
		return "squash-merged"
	case "rebase":
		return "rebase-merged"
	case "empty":
		return "no commits of its own"
	}
	return "merged"
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

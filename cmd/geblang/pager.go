package main

import (
	"io"
	"os"
	"os/exec"
	"strings"
)

// writePaged renders text to w, routing through a pager when stdout is
// an interactive terminal and paging is not disabled. Honors $PAGER,
// then falls back to `less -R`, then `more`. Falls back to a plain
// write when no pager is available or paging is off.
func writePaged(w io.Writer, text string, noPager bool) {
	if noPager || !isTerminal(os.Stdout) {
		io.WriteString(w, text)
		return
	}
	if runPager(text) {
		return
	}
	io.WriteString(w, text)
}

// runPager pipes text through the first available pager and reports
// whether one handled it.
func runPager(text string) bool {
	candidates := []string{}
	if p := os.Getenv("PAGER"); strings.TrimSpace(p) != "" {
		candidates = append(candidates, p)
	}
	candidates = append(candidates, "less -R", "more")
	for _, c := range candidates {
		fields := strings.Fields(c)
		if len(fields) == 0 {
			continue
		}
		path, err := exec.LookPath(fields[0])
		if err != nil {
			continue
		}
		cmd := exec.Command(path, fields[1:]...)
		cmd.Stdin = strings.NewReader(text)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run() == nil
	}
	return false
}

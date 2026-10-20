package connectmatrix

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// AllPassed reports whether every pair passed.
func AllPassed(rs []PairResult) bool {
	for _, r := range rs {
		if !r.Pass {
			return false
		}
	}
	return len(rs) > 0
}

// WriteTable prints one line per check. It contains no token.
func WriteTable(w io.Writer, rs []PairResult) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "PAIR\tCHECK\tRESULT\tSECONDS\tDETAIL")
	for _, r := range rs {
		for _, c := range r.Checks {
			secs := ""
			if c.Seconds > 0 {
				secs = fmt.Sprintf("%.1f", c.Seconds)
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Name, c.Name, strings.ToUpper(c.Result), secs, c.Detail)
		}
	}
	_ = tw.Flush()
	pass := 0
	for _, r := range rs {
		if r.Pass {
			pass++
		}
	}
	_, _ = fmt.Fprintf(w, "\n%d of %d pairs proved\n", pass, len(rs))
}

// WriteJSON writes the results as indented JSON.
func WriteJSON(w io.Writer, rs []PairResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"pass": AllPassed(rs), "pairs": rs})
}

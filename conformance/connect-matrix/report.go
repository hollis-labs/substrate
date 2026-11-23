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

// WriteMatrix prints caller rows against target columns, one status per cell,
// then a count per status. Pairs not named "caller->target" are listed as
// their own row. It contains no token.
func WriteMatrix(w io.Writer, rs []PairResult) {
	var callers, apps []string
	idx := func(l *[]string, v string) {
		for _, x := range *l {
			if x == v {
				return
			}
		}
		*l = append(*l, v)
	}
	cell := map[string]string{}
	counts := map[string]int{}
	for _, r := range rs {
		idx(&callers, r.Caller)
		idx(&apps, r.App)
		cell[r.Caller+"\x00"+r.App] = r.Status
		counts[r.Status]++
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "CALLER\\TARGET\t"+strings.Join(apps, "\t"))
	for _, c := range callers {
		row := []string{c}
		for _, a := range apps {
			v := cell[c+"\x00"+a]
			if v == "" {
				v = "-"
			}
			row = append(row, v)
		}
		_, _ = fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(w, "\npass=%d fail=%d not-enforced=%d n-a=%d\n", counts[StatusPass], counts[StatusFail], counts[StatusNotEnforced], counts[StatusNA])
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
	counts := map[string]int{}
	for _, r := range rs {
		counts[r.Status]++
	}
	return enc.Encode(map[string]any{"pass": AllPassed(rs), "summary": counts, "pairs": rs})
}

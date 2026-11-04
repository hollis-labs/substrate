package upgrade

import (
	"regexp"
	"strings"
)

// Release is one changelog section.
type Release struct {
	Version  string // "0.2.0", or "Unreleased"
	Body     string
	Breaking []BreakingNote
}

// BreakingNote is one bullet under "### Breaking". Migration is the id named by
// "(migration: id)", "none" for an explicit "(migration: none)", or "" when the
// bullet names nothing, which upgrade treats as unmigrated.
type BreakingNote struct{ Text, Migration string }

var (
	releaseHead = regexp.MustCompile(`(?m)^## \[([^\]]+)\]`)
	migrationTk = regexp.MustCompile(`\(migration:\s*([A-Za-z0-9_.-]+)\)`)
)

// ParseChangelog splits a Keep-a-Changelog file into releases, newest first.
func ParseChangelog(src string) []Release {
	idx := releaseHead.FindAllStringSubmatchIndex(src, -1)
	var out []Release
	for i, m := range idx {
		end := len(src)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		r := Release{Version: src[m[2]:m[3]], Body: strings.TrimSpace(src[m[1]:end])}
		r.Breaking = breaking(r.Body)
		out = append(out, r)
	}
	return out
}

// breaking returns the bullets under a "### Breaking" heading.
func breaking(body string) []BreakingNote {
	var notes []BreakingNote
	in := false
	var cur *strings.Builder
	flush := func() {
		if cur != nil {
			text := strings.TrimSpace(cur.String())
			n := BreakingNote{Text: text}
			if m := migrationTk.FindStringSubmatch(text); m != nil {
				n.Migration = m[1]
			}
			notes = append(notes, n)
			cur = nil
		}
	}
	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "### "):
			flush()
			in = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(line, "### ")), "Breaking")
		case in && strings.HasPrefix(line, "- "):
			flush()
			cur = &strings.Builder{}
			cur.WriteString(strings.TrimPrefix(line, "- "))
		case in && cur != nil && strings.HasPrefix(line, "  "):
			cur.WriteString(" " + strings.TrimSpace(line))
		case in && strings.TrimSpace(line) == "":
			flush()
		}
	}
	flush()
	return notes
}

// Between returns the releases after from up to and including to.
func Between(rs []Release, from, to Version) []Release {
	var out []Release
	for _, r := range rs {
		v, err := ParseVersion("v" + r.Version)
		if err != nil {
			continue // "Unreleased"
		}
		if v.Compare(from) > 0 && v.Compare(to) <= 0 {
			out = append(out, r)
		}
	}
	return out
}

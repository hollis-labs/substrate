package bom

import (
	"fmt"
	"strconv"
	"strings"
)

// satisfies reports whether version meets an npm-style range. It supports the
// forms the @hollis-labs kits use: exact versions, ^ and ~, comparators
// (>=, >, <=, <, =) joined by spaces, "||" alternatives, and "*". It is not a
// general npm semver implementation and refuses what it does not understand
// instead of guessing.
func satisfies(version, rng string) (bool, error) {
	v, err := parseNum(version)
	if err != nil {
		return false, err
	}
	for _, alt := range strings.Split(rng, "||") {
		ok, err := allMatch(v, strings.Fields(alt))
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

type num [3]int

func parseNum(s string) (num, error) {
	var n num
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 || s == "" {
		return n, fmt.Errorf("semver: cannot read %q", s)
	}
	for i, p := range parts {
		if p == "x" || p == "X" || p == "*" {
			break
		}
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 {
			return n, fmt.Errorf("semver: cannot read %q", s)
		}
		n[i] = x
	}
	return n, nil
}

func cmp(a, b num) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func allMatch(v num, terms []string) (bool, error) {
	if len(terms) == 0 {
		return true, nil // an empty range means any version
	}
	for _, t := range terms {
		ok, err := matchOne(v, t)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func matchOne(v num, t string) (bool, error) {
	switch {
	case t == "*" || t == "x" || t == "latest":
		return true, nil
	case strings.HasPrefix(t, "^"):
		base, err := parseNum(t[1:])
		if err != nil {
			return false, err
		}
		upper := num{base[0] + 1}
		if base[0] == 0 { // 0.x: the minor is the breaking boundary; 0.0.x pins the patch
			upper = num{0, base[1] + 1}
			if base[1] == 0 {
				upper = num{0, 0, base[2] + 1}
			}
		}
		return cmp(v, base) >= 0 && cmp(v, upper) < 0, nil
	case strings.HasPrefix(t, "~"):
		base, err := parseNum(t[1:])
		if err != nil {
			return false, err
		}
		return cmp(v, base) >= 0 && cmp(v, num{base[0], base[1] + 1}) < 0, nil
	}
	for _, op := range []string{">=", "<=", ">", "<", "="} {
		if strings.HasPrefix(t, op) {
			base, err := parseNum(t[len(op):])
			if err != nil {
				return false, err
			}
			c := cmp(v, base)
			switch op {
			case ">=":
				return c >= 0, nil
			case "<=":
				return c <= 0, nil
			case ">":
				return c > 0, nil
			case "<":
				return c < 0, nil
			}
			return c == 0, nil
		}
	}
	base, err := parseNum(t) // exact
	if err != nil {
		return false, err
	}
	return cmp(v, base) == 0, nil
}

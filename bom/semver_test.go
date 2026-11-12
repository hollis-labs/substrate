package bom

import "testing"

func TestSatisfiesTheRangesTheKitsUse(t *testing.T) {
	for _, c := range []struct {
		v, r string
		want bool
	}{
		{"0.5.1", "^0.5.0", true},
		{"0.6.0", "^0.5.0", false}, // 0.x: the minor is the boundary
		{"0.1.0", "^0.1.0", true},
		{"0.2.0", "^0.1.0", false},
		{"1.4.0", "^1.2.0", true},
		{"2.0.0", "^1.2.0", false},
		{"0.0.3", "^0.0.3", true},
		{"0.0.4", "^0.0.3", false},
		{"0.6.0", "^0.5.0 || ^0.6.0", true},
		{"0.7.0", "^0.5.0 || ^0.6.0", false},
		{"0.5.9", "~0.5.1", true},
		{"0.6.0", "~0.5.1", false},
		{"1.2.3", ">=1.0.0 <2.0.0", true},
		{"2.0.0", ">=1.0.0 <2.0.0", false},
		{"3.0.0", "*", true},
		{"0.3.0", "0.3.0", true},
		{"0.3.1", "0.3.0", false},
	} {
		got, err := satisfies(c.v, c.r)
		if err != nil || got != c.want {
			t.Errorf("satisfies(%s, %q) = %v, %v; want %v", c.v, c.r, got, err, c.want)
		}
	}
	if _, err := satisfies("1.0.0", "git+https://x"); err == nil {
		t.Error("a range the evaluator does not understand must be an error, not a guess")
	}
}

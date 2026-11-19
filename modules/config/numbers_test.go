package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestIntegersBecomeFloat64(t *testing.T) {
	override := `
m:
  top: 7
  zero: 0
  neg: -3
  frac: 1.5
  flag: true
  name: "12"
  nested:
    deep:
      n: 42
  ints: [1, 2, -3, 0]
  mixed: [1, 2.5, "s", true]
  items:
    - {a: 1, b: {c: 2}}
    - {a: 3}
  nulled: ~
`
	got, err := run(t, map[string]any{"m": map[string]any{"top": float64(1), "nulled": float64(9)}}, override)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"m": map[string]any{
		"top": float64(7), "zero": float64(0), "neg": float64(-3), "frac": 1.5, "flag": true, "name": "12",
		"nested": map[string]any{"deep": map[string]any{"n": float64(42)}},
		"ints":   []any{float64(1), float64(2), float64(-3), float64(0)},
		"mixed":  []any{float64(1), 2.5, "s", true},
		"items": []any{
			map[string]any{"a": float64(1), "b": map[string]any{"c": float64(2)}},
			map[string]any{"a": float64(3)},
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

func TestBigIntegerRefusedNamingTheKey(t *testing.T) {
	cases := []struct{ name, yaml, key string }{
		{"above 2^53", "m:\n  big: 9007199254740993\n", `"m.big"`},
		{"below -2^53", "m:\n  big: -9007199254740993\n", `"m.big"`},
		{"beyond int64", "m:\n  big: 18446744073709551615\n", `"m.big"`},
		{"nested", "m:\n  a:\n    big: 9007199254740993\n", `"m.a.big"`},
		{"inside a list", "m:\n  xs: [1, 9007199254740993]\n", `"m.xs[1]"`},
		{"in a list's map", "m:\n  xs:\n    - {n: 9007199254740993}\n", `"m.xs[0].n"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := run(t, map[string]any{}, c.yaml)
			if err == nil || !strings.Contains(err.Error(), c.key) {
				t.Fatalf("want an error naming %s, got %v", c.key, err)
			}
		})
	}
}

func TestIntegerAtTheExactLimitIsKept(t *testing.T) {
	got, err := run(t, map[string]any{}, "m:\n  hi: 9007199254740992\n  lo: -9007199254740992\n")
	if err != nil {
		t.Fatal(err)
	}
	m := got["m"].(map[string]any)
	if m["hi"] != float64(1<<53) || m["lo"] != -float64(1<<53) {
		t.Fatalf("m = %#v", m)
	}
}

func TestNormalizeCoversEveryIntegerKind(t *testing.T) {
	for _, v := range []any{int(5), int8(5), int16(5), int32(5), int64(5), uint(5), uint8(5), uint16(5), uint32(5), uint64(5)} {
		got, err := normalize(v, "k")
		if err != nil || got != float64(5) {
			t.Errorf("%T: got %#v, %v", v, got, err)
		}
	}
	if _, err := normalize(uint64(1<<53+1), "k"); err == nil {
		t.Error("uint64 above 2^53 was accepted")
	}
	if _, err := normalize(uint(1<<53+1), "k"); err == nil {
		t.Error("uint above 2^53 was accepted")
	}
	if _, err := normalize(int64(-(1<<53 + 1)), "k"); err == nil {
		t.Error("int64 below -2^53 was accepted")
	}
}

func TestNormalizeDoesNotMutateItsInput(t *testing.T) {
	in := map[string]any{"xs": []any{1, map[string]any{"a": 2}}}
	if _, err := normalize(in, "k"); err != nil {
		t.Fatal(err)
	}
	if in["xs"].([]any)[0] != 1 || in["xs"].([]any)[1].(map[string]any)["a"] != 2 {
		t.Fatalf("input mutated: %#v", in)
	}
}

func TestNonStringKeyRefusedNamingThePath(t *testing.T) {
	cases := []struct{ name, yaml, path string }{
		{"in a map", "m:\n  a:\n    1: x\n", `"m.a"`},
		{"in a list's map", "m:\n  xs:\n    - {2: y}\n", `"m.xs[0]"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := run(t, map[string]any{}, c.yaml)
			if err == nil || !strings.Contains(err.Error(), c.path) || !strings.Contains(err.Error(), "not a string") {
				t.Fatalf("want an error naming %s, got %v", c.path, err)
			}
		})
	}
}

// Documented limit: an integer literal beyond 64 bits arrives from yaml.v3
// already as a float64, so it is indistinguishable from a float and is not refused.
func TestIntegerBeyond64BitsPassesAsFloat(t *testing.T) {
	got, err := run(t, map[string]any{}, "m:\n  big: 100000000000000000000000\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["m"].(map[string]any)["big"].(float64); !ok {
		t.Fatalf("got %#v", got)
	}
}

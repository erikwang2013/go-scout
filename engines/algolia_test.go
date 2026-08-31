package engines

import (
	"testing"
	"time"
)

// Regression for the algolia.go filter fix: algoliaLit renders Algolia filter
// literals (strings double-quoted and escaped, numbers/bools bare, timestamps
// as unix seconds), and algoliaFilters joins them in field order.
func TestAlgoliaLit(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"string", "foo", `"foo"`},
		{"empty string", "", `""`},
		{"string escapes quote", `a"b`, `"a\"b"`},
		{"string escapes backslash", `a\b`, `"a\\b"`},
		{"int", 42, "42"},
		{"int64", int64(42), "42"},
		{"float64 whole", 42.0, "42"},
		{"float64 fraction", 1.5, "1.5"},
		{"float32", float32(1.5), "1.5"},
		{"true", true, "true"},
		{"false", false, "false"},
		{"nil", nil, "null"},
		{"time", time.Unix(1700000000, 0), "1700000000"},
		{"default falls back to sprint", struct{ X int }{3}, `"{3}"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := algoliaLit(c.in); got != c.want {
				t.Errorf("algoliaLit(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestAlgoliaFiltersRendering(t *testing.T) {
	b := newBuilder(t, nil)
	b.Where("category", "shoes")
	b.Where("active", true)
	b.Where("price", 10)
	b.Where("name", `a"b\c`)
	b.Where("created", time.Unix(1700000000, 0))
	b.WhereIn("status", []any{"a", "b"})

	got := algoliaFilters(b)
	want := `active=true AND category="shoes" AND created=1700000000 AND name="a\"b\\c" AND price=10 AND status IN ["a", "b"]`
	if got != want {
		t.Errorf("algoliaFilters:\n got %s\nwant %s", got, want)
	}
}

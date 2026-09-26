package engines

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/erikwang2013/go-scout"
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

// SCOUT_IDENTIFY carries laravel-scout's defaultAlgoliaHeaders: with it on, the
// user key rides as X-Algolia-UserToken and the client IP as X-Forwarded-For —
// public addresses only.
func TestAlgoliaIdentifyHeaders(t *testing.T) {
	t.Setenv("ALGOLIA_APP_ID", "APPID")
	t.Setenv("ALGOLIA_SECRET", "ADMINKEY")

	// The identity is on the context here on purpose: identify off must drop it,
	// otherwise this test cannot tell "switch off" from "no identity supplied".
	who := scout.WithClientIP(scout.WithUser(context.Background(), 7), "8.8.8.8")
	off := newAlgolia(scout.DefaultConfig())
	if h := off.headers(who); h["X-Algolia-UserToken"] != "" || h["X-Forwarded-For"] != "" {
		t.Errorf("identify off must not forward identity, got %v", h)
	}

	t.Setenv("SCOUT_IDENTIFY", "1")
	on := newAlgolia(scout.DefaultConfig())
	if !on.identify {
		t.Fatal("SCOUT_IDENTIFY=1 did not reach the engine")
	}
	ctx := scout.WithClientIP(scout.WithUser(context.Background(), 7), "8.8.8.8")
	h := on.headers(ctx)
	if h["X-Algolia-UserToken"] != "7" {
		t.Errorf("X-Algolia-UserToken = %q, want 7", h["X-Algolia-UserToken"])
	}
	if h["X-Forwarded-For"] != "8.8.8.8" {
		t.Errorf("X-Forwarded-For = %q, want 8.8.8.8", h["X-Forwarded-For"])
	}
	if h["X-Algolia-Application-Id"] != "APPID" || h["X-Algolia-API-Key"] != "ADMINKEY" {
		t.Errorf("auth headers lost: %v", h)
	}

	t.Run("private ip is not forwarded", func(t *testing.T) {
		h := on.headers(scout.WithClientIP(scout.WithUser(context.Background(), 7), "10.1.2.3"))
		if _, ok := h["X-Forwarded-For"]; ok {
			t.Errorf("private IP leaked: %v", h)
		}
		if h["X-Algolia-UserToken"] != "7" {
			t.Errorf("user token dropped with the IP: %v", h)
		}
	})

	t.Run("no identity on the context", func(t *testing.T) {
		h := on.headers(context.Background())
		if _, ok := h["X-Algolia-UserToken"]; ok {
			t.Errorf("token without WithUser: %v", h)
		}
		if _, ok := h["X-Forwarded-For"]; ok {
			t.Errorf("IP without WithClientIP: %v", h)
		}
	})
}

// The identity has to reach the wire, not just the header map: run a real search
// against a stub server and read what the engine actually sent.
//
// The engine's base URL is derived from the app id (algolia.host is read by
// base() but DefaultConfig has no key for it), so the test rewrites the outgoing
// request onto the stub instead of moving the engine.
func TestAlgoliaIdentifyOnTheWire(t *testing.T) {
	srv, rec := mtsCapture(t, `{"hits":[],"nbHits":0}`)
	t.Setenv("ALGOLIA_APP_ID", "APPID")
	t.Setenv("ALGOLIA_SECRET", "ADMINKEY")
	t.Setenv("SCOUT_IDENTIFY", "1")
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	e := newAlgolia(scout.DefaultConfig())
	e.client = &http.Client{Transport: rewriteTo{target: target}}
	ctx := scout.WithClientIP(scout.WithUser(context.Background(), 7), "8.8.8.8")
	if _, err := e.Search(ctx, newBuilder(t, nil)); err != nil {
		t.Fatal(err)
	}
	if got := rec.header.Get("X-Algolia-UserToken"); got != "7" {
		t.Errorf("wire X-Algolia-UserToken = %q, want 7", got)
	}
	if got := rec.header.Get("X-Forwarded-For"); got != "8.8.8.8" {
		t.Errorf("wire X-Forwarded-For = %q, want 8.8.8.8", got)
	}
	if got := rec.header.Get("X-Algolia-API-Key"); got != "ADMINKEY" {
		t.Errorf("wire auth lost: %q", got)
	}
}

// rewriteTo sends every request to target, keeping path and headers.
type rewriteTo struct{ target *url.URL }

func (rw rewriteTo) RoundTrip(r *http.Request) (*http.Response, error) {
	out := r.Clone(r.Context())
	u := *r.URL
	u.Scheme, u.Host = rw.target.Scheme, rw.target.Host
	out.URL = &u
	return http.DefaultTransport.RoundTrip(out)
}

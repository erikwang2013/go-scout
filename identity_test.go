package scout

import (
	"context"
	"testing"
)

func TestUserRoundTrip(t *testing.T) {
	ctx := WithUser(context.Background(), 42)
	got, ok := UserFrom(ctx)
	if !ok || got != "42" {
		t.Errorf("UserFrom = %q, %v; want \"42\", true", got, ok)
	}
	if _, ok := UserFrom(context.Background()); ok {
		t.Error("UserFrom without WithUser should report false")
	}
	if _, ok := UserFrom(nil); ok {
		t.Error("UserFrom(nil) should report false, not panic")
	}
	if ctx := WithUser(nil, "u1"); ctx == nil {
		t.Error("WithUser(nil, ...) should still return a usable context")
	}
}

// PublicIP is the Go equivalent of PHP's FILTER_FLAG_NO_PRIV_RANGE |
// FILTER_FLAG_NO_RES_RANGE gate in front of the X-Forwarded-For header.
func TestPublicIP(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"8.8.8.8", "8.8.8.8", true},
		{"1.2.3.4:5678", "1.2.3.4", true},
		{"[2001:4860:4860::8888]:443", "2001:4860:4860::8888", true},
		{"::ffff:8.8.8.8", "8.8.8.8", true},
		{"  8.8.8.8  ", "8.8.8.8", true},

		{"", "", false},
		{"not-an-ip", "", false},
		{"10.0.0.1", "", false},    // private
		{"172.16.5.4", "", false},  // private
		{"192.168.1.1", "", false}, // private
		{"127.0.0.1", "", false},   // loopback
		{"169.254.1.1", "", false}, // link-local
		{"100.64.0.1", "", false},  // carrier-grade NAT
		{"192.0.2.1", "", false},   // TEST-NET-1
		{"203.0.113.7", "", false}, // TEST-NET-3
		{"198.18.0.1", "", false},  // benchmarking
		{"240.0.0.1", "", false},   // reserved
		{"0.0.0.0", "", false},     // unspecified
		{"255.255.255.255", "", false},
		{"::1", "", false},         // loopback
		{"fd00::1", "", false},     // unique local
		{"fe80::1", "", false},     // link-local
		{"2001:db8::1", "", false}, // documentation
		{"ff02::1", "", false},     // multicast
	}
	for _, c := range cases {
		got, ok := PublicIP(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("PublicIP(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestClientIPFrom(t *testing.T) {
	ctx := WithClientIP(context.Background(), "8.8.8.8")
	if got, ok := ClientIPFrom(ctx); !ok || got != "8.8.8.8" {
		t.Errorf("ClientIPFrom = %q, %v", got, ok)
	}
	// A private address is dropped here, so engines never see it.
	if got, ok := ClientIPFrom(WithClientIP(context.Background(), "192.168.1.10")); ok {
		t.Errorf("private IP surfaced as %q", got)
	}
	if _, ok := ClientIPFrom(nil); ok {
		t.Error("ClientIPFrom(nil) should report false, not panic")
	}
}

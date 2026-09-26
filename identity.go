package scout

import (
	"context"
	"net/netip"
	"strings"
)

// Who is searching. The PHP original reads this off the request
// (request()->user(), request()->ip()); a Go library has no request object, so
// the caller puts the identity on the context and the engines that accept
// per-user attribution read it back. Only Algolia consumes it today, and only
// when SCOUT_IDENTIFY is on — see EngineManager::defaultAlgoliaHeaders in
// laravel-scout.

type identityCtxKey struct{}
type ipCtxKey struct{}

// WithUser returns a context carrying the end user's key. The engine forwards it
// as X-Algolia-UserToken when identify is enabled, which Algolia uses for
// secured API keys, per-user rate limits and analytics attribution.
func WithUser(ctx context.Context, key any) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, identityCtxKey{}, KeyString(key))
}

// UserFrom returns the user key stored by WithUser.
func UserFrom(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	s, ok := ctx.Value(identityCtxKey{}).(string)
	return s, ok && s != ""
}

// WithClientIP returns a context carrying the end user's IP. Only public
// addresses are forwarded: ClientIPFrom drops private, loopback, link-local and
// reserved ones, mirroring PHP's FILTER_FLAG_NO_PRIV_RANGE|FILTER_FLAG_NO_RES_RANGE.
func WithClientIP(ctx context.Context, ip string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ipCtxKey{}, ip)
}

// ClientIPFrom returns the public client IP stored by WithClientIP. The second
// result is false when no IP was set or the one set is not publicly routable.
func ClientIPFrom(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	raw, _ := ctx.Value(ipCtxKey{}).(string)
	return PublicIP(raw)
}

// reserved are the non-routable blocks PHP's FILTER_FLAG_NO_RES_RANGE rejects
// that netip does not already classify as private, loopback, link-local or
// multicast.
// ponytail: the common blocks only; add prefixes here if a proxy in front of the
// app hands out addresses from an unusual reserved range.
var reserved = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, incl. 255.255.255.255
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
}

// PublicIP reports whether ip is a publicly routable address and returns it in
// canonical form ("::ffff:1.2.3.4" becomes "1.2.3.4"). The port, if the caller
// passed "host:port", is dropped.
func PublicIP(ip string) (string, bool) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return "", false
	}
	// Accept "1.2.3.4:5678" / "[::1]:443" as well as a bare address.
	if addr, err := netip.ParseAddrPort(ip); err == nil {
		ip = addr.Addr().String()
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return "", false
	}
	addr = addr.Unmap().WithZone("")
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsUnspecified() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() ||
		addr.IsInterfaceLocalMulticast() {
		return "", false
	}
	for _, p := range reserved {
		if p.Contains(addr) {
			return "", false
		}
	}
	return addr.String(), true
}

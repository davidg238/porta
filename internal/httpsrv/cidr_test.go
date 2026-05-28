// internal/httpsrv/cidr_test.go
package httpsrv

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllowCIDRParsesRFC1918AndIPv6(t *testing.T) {
	in := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "::1/128"}
	out, err := AllowCIDR(in)
	if err != nil {
		t.Fatalf("AllowCIDR: %v", err)
	}
	if len(out) != 5 {
		t.Errorf("got %d nets, want 5", len(out))
	}
}

func TestAllowCIDRRejectsGarbage(t *testing.T) {
	if _, err := AllowCIDR([]string{"not-a-cidr"}); err == nil {
		t.Error("expected error on garbage")
	}
	if _, err := AllowCIDR([]string{"10.0.0.0/8", "bad"}); err == nil {
		t.Error("expected error when one entry is bad")
	}
}

func TestAllowCIDRFiltersEmptyAndWhitespace(t *testing.T) {
	out, err := AllowCIDR([]string{"", "  ", "10.0.0.0/8", "\t"})
	if err != nil {
		t.Fatalf("AllowCIDR: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("got %d nets, want 1 (empties filtered)", len(out))
	}
}

func TestAllowCIDRAllEmptyReturnsNil(t *testing.T) {
	out, err := AllowCIDR([]string{"", "  "})
	if err != nil {
		t.Fatalf("AllowCIDR: %v", err)
	}
	if out != nil {
		t.Errorf("got %v, want nil (all-empty = disable allowlist)", out)
	}
}

// okHandler is the dummy downstream for middleware tests.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func mustNets(t *testing.T, cidrs ...string) []*net.IPNet {
	t.Helper()
	nets, err := AllowCIDR(cidrs)
	if err != nil {
		t.Fatal(err)
	}
	return nets
}

func TestAllowlistMiddlewareEmptyServesAnyPeer(t *testing.T) {
	mw := AllowlistMiddleware(nil)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	w := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200 (nil allowed = serve any)", w.Code)
	}
}

func TestAllowlistMiddlewareEmptySliceServesAnyPeer(t *testing.T) {
	mw := AllowlistMiddleware([]*net.IPNet{})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	w := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200 (empty allowed = serve any)", w.Code)
	}
}

func TestAllowlistMiddlewareAcceptsAllowedIPv4(t *testing.T) {
	mw := AllowlistMiddleware(mustNets(t, "192.168.0.0/16"))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.5:1234"
	w := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
}

func TestAllowlistMiddlewareRejectsDisallowedIPv4(t *testing.T) {
	mw := AllowlistMiddleware(mustNets(t, "192.168.0.0/16"))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	w := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", w.Code)
	}
}

func TestAllowlistMiddlewareIPv4MappedIPv6Matches(t *testing.T) {
	mw := AllowlistMiddleware(mustNets(t, "192.168.0.0/16"))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "[::ffff:192.168.1.5]:1234"
	w := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200 (IPv4-mapped IPv6 should match v4 CIDR)", w.Code)
	}
}

func TestAllowlistMiddlewareIPv6Matches(t *testing.T) {
	mw := AllowlistMiddleware(mustNets(t, "::1/128"))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "[::1]:1234"
	w := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
}

func TestAllowlistMiddlewareMalformedRemoteAddrRejected(t *testing.T) {
	mw := AllowlistMiddleware(mustNets(t, "192.168.0.0/16"))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "not-an-addr"
	w := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403 (defense)", w.Code)
	}
}

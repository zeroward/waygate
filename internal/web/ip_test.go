package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIPIgnoresXFFFromPublicPeer(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.9:443"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	if got := clientIP(r, true); got != "203.0.113.9" {
		t.Fatalf("public peer XFF: %s", got)
	}
}

func TestClientIPTrustsXFFFromPrivatePeer(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "172.19.0.8:444"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	if got := clientIP(r, true); got != "198.51.100.1" {
		t.Fatalf("private peer XFF: %s", got)
	}
}

func TestClientIPIgnoresXFFWhenTrustProxyFalse(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "172.19.0.8:444"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	if got := clientIP(r, false); got != "172.19.0.8" {
		t.Fatalf("trustProxy false: %s", got)
	}
}

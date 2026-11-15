package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestLoginThrottleCountsOnlyFailures(t *testing.T) {
	l := newLoginThrottle()
	now := time.Now()
	for i := 0; i < loginMaxFailsPerAccount*3; i++ {
		if l.blocked("10.0.0.1", "ana", now) {
			t.Fatalf("successful logins must not block: attempt %d", i)
		}
		l.succeed("10.0.0.1", "ana")
	}
	for i := 0; i < loginMaxFailsPerAccount; i++ {
		if l.blocked("10.0.0.1", "ana", now) {
			t.Fatalf("blocked after %d failures, ceiling is %d", i, loginMaxFailsPerAccount)
		}
		l.fail("10.0.0.1", "ana", now)
	}
	if !l.blocked("10.0.0.1", "ana", now) {
		t.Fatal("account must be blocked after reaching the ceiling")
	}
	if !l.blocked("10.0.0.1", " ANA ", now) {
		t.Fatal("login case and spaces must not bypass the ceiling")
	}
	if l.blocked("10.0.0.2", "ana", now) {
		t.Fatal("another address must not be blocked by this one")
	}
	if l.blocked("10.0.0.1", "bruno", now) {
		t.Fatal("another login from the same address must not be blocked yet")
	}
	if l.blocked("10.0.0.1", "ana", now.Add(loginFailWindow)) {
		t.Fatal("failures must expire after the window")
	}
}

func TestLoginThrottleSuccessResetsAccountButNotIP(t *testing.T) {
	l := newLoginThrottle()
	now := time.Now()
	for i := 0; i < loginMaxFailsPerAccount-1; i++ {
		l.fail("10.0.0.1", "ana", now)
	}
	l.succeed("10.0.0.1", "ana")
	for i := 0; i < loginMaxFailsPerAccount-1; i++ {
		l.fail("10.0.0.1", "ana", now)
	}
	if l.blocked("10.0.0.1", "ana", now) {
		t.Fatal("a successful login must reset the account counter")
	}

	// One client cycling through accounts hits the per-IP ceiling, and a valid
	// login in between does not release it.
	l = newLoginThrottle()
	for i := 0; i < loginMaxFailsPerIP; i++ {
		l.fail("10.0.0.9", fmt.Sprintf("user-%d", i), now)
		if i == loginMaxFailsPerIP/2 {
			l.succeed("10.0.0.9", "ana")
		}
	}
	if !l.blocked("10.0.0.9", "never-tried", now) {
		t.Fatal("per-IP ceiling must block any login from that address")
	}
	if l.blocked("10.0.0.8", "never-tried", now) {
		t.Fatal("per-IP ceiling must not affect other addresses")
	}
}

func TestLoginThrottleStaysBounded(t *testing.T) {
	l := newLoginThrottle()
	now := time.Now()
	for i := 0; i < loginThrottleMaxKeys*2; i++ {
		l.fail(fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255), "x", now)
	}
	l.mu.Lock()
	n := len(l.fails)
	l.mu.Unlock()
	if n > loginThrottleMaxKeys {
		t.Fatalf("throttle map grew to %d keys, cap is %d", n, loginThrottleMaxKeys)
	}
	l.fail("10.0.0.1", "y", now.Add(loginFailWindow+loginThrottlePruneEvery))
	l.mu.Lock()
	n = len(l.fails)
	l.mu.Unlock()
	if n != 2 {
		t.Fatalf("expired keys must be pruned: %d keys left, want 2", n)
	}
}

func TestClientIPTrustsOnlyConfiguredProxyHops(t *testing.T) {
	req := func(remote string, xff ...string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/login", nil)
		r.RemoteAddr = remote
		for _, v := range xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		return r
	}
	cases := []struct {
		name string
		r    *http.Request
		hops int
		want string
	}{
		{"no proxy ignores forged header", req("203.0.113.5:4000", "1.2.3.4"), 0, "203.0.113.5"},
		{"one proxy takes right-most", req("10.1.0.1:4000", "1.2.3.4, 198.51.100.7"), 1, "198.51.100.7"},
		{"two proxies", req("10.1.0.1:4000", "1.2.3.4, 198.51.100.7, 10.2.0.1"), 2, "198.51.100.7"},
		{"repeated headers are joined", req("10.1.0.1:4000", "1.2.3.4", "198.51.100.7"), 1, "198.51.100.7"},
		{"ipv6", req("10.1.0.1:4000", "2001:db8::1"), 1, "2001:db8::1"},
		{"missing header falls back", req("10.1.0.1:4000"), 1, "10.1.0.1"},
		{"fewer entries than hops falls back", req("10.1.0.1:4000", "198.51.100.7"), 2, "10.1.0.1"},
		{"garbage falls back", req("10.1.0.1:4000", "not-an-ip"), 1, "10.1.0.1"},
		{"remote without port", req("10.1.0.1"), 0, "10.1.0.1"},
	}
	for _, c := range cases {
		if got := clientIP(c.r, c.hops); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestLoginClientIPPrefersConfiguredEdgeHeader(t *testing.T) {
	req := func(trueClient string, xff string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/login", nil)
		r.RemoteAddr = "10.1.0.1:4000"
		if trueClient != "" {
			r.Header.Set("True-Client-IP", trueClient)
		}
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}
	t.Setenv("TRUSTED_PROXY_HOPS", "")
	t.Setenv("CLIENT_IP_HEADER", "")
	if got := loginClientIP(req("198.51.100.7", "")); got != "10.1.0.1" {
		t.Fatalf("header must be ignored unless configured: got %s", got)
	}
	t.Setenv("CLIENT_IP_HEADER", "True-Client-IP")
	if got := loginClientIP(req("198.51.100.7", "1.2.3.4")); got != "198.51.100.7" {
		t.Fatalf("configured header: got %s", got)
	}
	if got := loginClientIP(req("not-an-ip", "")); got != "10.1.0.1" {
		t.Fatalf("invalid header value must fall back: got %s", got)
	}
	if got := loginClientIP(req("", "")); got != "10.1.0.1" {
		t.Fatalf("missing header must fall back: got %s", got)
	}
	t.Setenv("TRUSTED_PROXY_HOPS", "1")
	if got := loginClientIP(req("", "203.0.113.9")); got != "203.0.113.9" {
		t.Fatalf("missing header falls back to TRUSTED_PROXY_HOPS: got %s", got)
	}
}

func TestTrustedProxyHopsDefaultsToZero(t *testing.T) {
	for value, want := range map[string]int{"": 0, "abc": 0, "-1": 0, "1": 1, " 2 ": 2} {
		t.Setenv("TRUSTED_PROXY_HOPS", value)
		if got := trustedProxyHops(); got != want {
			t.Errorf("TRUSTED_PROXY_HOPS=%q: got %d, want %d", value, got, want)
		}
	}
}

// End to end through /api/login: correct logins never count, failures block
// only that login and address, and a blocked client is refused even with the
// right password.
func TestLoginEndpointThrottlesFailuresPerLoginAndAddress(t *testing.T) {
	db := openTestDatabase(t)
	if err := runMigrations(context.Background(), db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	a := &app{db: db}
	createTestUser(t, a, "throttle-ana", "senha-certa", "Diretor", true)
	createTestUser(t, a, "throttle-bruno", "senha-certa", "Diretor", true)
	authThrottle.reset()
	t.Cleanup(authThrottle.reset)
	handler := newHTTPServer("127.0.0.1:0", a).Handler

	login := func(remote, user, pass string) int {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"login": user, "password": pass})
		req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
		req.RemoteAddr = remote
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr.Code
	}

	for i := 0; i < loginMaxFailsPerAccount+2; i++ {
		if code := login("192.0.2.10:1000", "throttle-ana", "senha-certa"); code != http.StatusOK {
			t.Fatalf("correct login %d from a shared address: got %d", i, code)
		}
	}
	for i := 0; i < loginMaxFailsPerAccount; i++ {
		if code := login("192.0.2.10:1000", "throttle-ana", "errada"); code != http.StatusUnauthorized {
			t.Fatalf("failure %d: got %d", i, code)
		}
	}
	if code := login("192.0.2.10:1000", "throttle-ana", "senha-certa"); code != http.StatusTooManyRequests {
		t.Fatalf("blocked account with correct password: got %d, want 429", code)
	}
	if code := login("192.0.2.10:1000", "throttle-bruno", "senha-certa"); code != http.StatusOK {
		t.Fatalf("another login from the same address: got %d", code)
	}
	if code := login("192.0.2.20:1000", "throttle-ana", "senha-certa"); code != http.StatusOK {
		t.Fatalf("same login from another address: got %d", code)
	}
}

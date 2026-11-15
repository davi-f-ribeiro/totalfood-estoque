package main

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Login throttling counts failed attempts only, so a busy shift logging in from
// one shared address (or one proxy) never locks everybody out.
//
// Two ceilings apply inside a sliding window:
//   - per login and client IP: stops guessing one account's password;
//   - per client IP, any login: stops one client trying many accounts.
//
// State lives in memory, per instance. With several instances each one counts
// on its own, so the effective ceiling is multiplied by the instance count.
const (
	loginFailWindow         = 15 * time.Minute
	loginMaxFailsPerAccount = 10
	loginMaxFailsPerIP      = 50
	loginThrottleMaxKeys    = 10000
	loginThrottleKeyMaxLen  = 64
	loginThrottlePruneEvery = time.Minute
)

type loginThrottle struct {
	mu        sync.Mutex
	fails     map[string][]time.Time
	lastPrune time.Time
}

var authThrottle = newLoginThrottle()

func newLoginThrottle() *loginThrottle { return &loginThrottle{fails: map[string][]time.Time{}} }

func throttleIPKey(ip string) string { return "ip\x00" + ip }
func throttleAccountKey(ip, login string) string {
	login = strings.ToLower(strings.TrimSpace(login))
	if len(login) > loginThrottleKeyMaxLen {
		login = login[:loginThrottleKeyMaxLen]
	}
	return "acct\x00" + ip + "\x00" + login
}

// recent drops timestamps older than the window and stores the result.
func (l *loginThrottle) recent(key string, now time.Time) []time.Time {
	xs := l.fails[key]
	kept := xs[:0]
	for _, t := range xs {
		if now.Sub(t) < loginFailWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.fails, key)
		return nil
	}
	l.fails[key] = kept
	return kept
}

// blocked reports whether this attempt must be refused before any password check.
func (l *loginThrottle) blocked(ip, login string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	return len(l.recent(throttleIPKey(ip), now)) >= loginMaxFailsPerIP ||
		len(l.recent(throttleAccountKey(ip, login), now)) >= loginMaxFailsPerAccount
}

func (l *loginThrottle) fail(ip, login string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, key := range []string{throttleIPKey(ip), throttleAccountKey(ip, login)} {
		l.fails[key] = append(l.recent(key, now), now)
	}
	l.pruneLocked(now)
}

// succeed clears the account counter. The per-IP counter is kept, so a valid
// login does not reset the brake on a client that is cycling through accounts.
func (l *loginThrottle) succeed(ip, login string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, throttleAccountKey(ip, login))
}

func (l *loginThrottle) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails = map[string][]time.Time{}
	l.lastPrune = time.Time{}
}

// pruneLocked removes expired keys periodically, and keeps the map bounded when
// many addresses fail at once. Per-IP keys are evicted last.
func (l *loginThrottle) pruneLocked(now time.Time) {
	if len(l.fails) <= loginThrottleMaxKeys && now.Sub(l.lastPrune) < loginThrottlePruneEvery {
		return
	}
	l.lastPrune = now
	for key := range l.fails {
		l.recent(key, now)
	}
	for _, prefix := range []string{"acct\x00", "ip\x00"} {
		for key := range l.fails {
			if len(l.fails) <= loginThrottleMaxKeys {
				return
			}
			if strings.HasPrefix(key, prefix) {
				delete(l.fails, key)
			}
		}
	}
}

// clientIP returns the address used for login throttling. With
// TRUSTED_PROXY_HOPS=0 (default) it is the TCP peer. With N trusted proxies in
// front of the app, it is the Nth address from the right of X-Forwarded-For:
// entries further left were written by the client and cannot be trusted.
func clientIP(r *http.Request, hops int) string {
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || remote == "" {
		remote = r.RemoteAddr
	}
	if hops <= 0 {
		return remote
	}
	var parts []string
	for _, header := range r.Header.Values("X-Forwarded-For") {
		for _, p := range strings.Split(header, ",") {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
	}
	if len(parts) < hops {
		return remote
	}
	candidate := parts[len(parts)-hops]
	if net.ParseIP(candidate) == nil {
		return remote
	}
	return candidate
}

// loginClientIP picks the address used for login throttling. CLIENT_IP_HEADER
// names a header written by a trusted edge that the client cannot set, such as
// Cloudflare's True-Client-IP in front of Render. Only set it when every request
// passes through that edge; otherwise a client can choose its own address.
// Without it, or when the header is missing or invalid, TRUSTED_PROXY_HOPS applies.
func loginClientIP(r *http.Request) string {
	if name := strings.TrimSpace(os.Getenv("CLIENT_IP_HEADER")); name != "" {
		if v := strings.TrimSpace(r.Header.Get(name)); net.ParseIP(v) != nil {
			return v
		}
	}
	return clientIP(r, trustedProxyHops())
}

func trustedProxyHops() int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("TRUSTED_PROXY_HOPS")))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

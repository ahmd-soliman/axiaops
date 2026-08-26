// Package httpip extracts the client IP from an *http.Request in a way that
// resists header-spoofing by upstream clients.
//
// Threat: nginx and the ECS Express ALB both *append* the connecting peer's IP to
// whatever X-Forwarded-For header the client sent. So a request from
// `attacker-ip` carrying `X-Forwarded-For: 1.2.3.4` arrives at the backend
// as `X-Forwarded-For: 1.2.3.4, attacker-ip`. Taking the leftmost token
// returns `1.2.3.4` — attacker-controlled — which lets an attacker rotate
// spoofed values to defeat per-IP rate limits and to plant chosen IPs in
// audit/session forensics.
//
// We instead trust, in order:
//  1. X-Real-IP — set by nginx via `proxy_set_header X-Real-IP $remote_addr`,
//     which unconditionally overwrites any client-supplied value. Reliable
//     in the staging shape; not set by the ECS Express ALB.
//  2. The rightmost token of X-Forwarded-For — the one our trusted proxy
//     added, i.e. the actual peer that connected to it.
//
//     Self-hosted / dev-staging funnels HTTP through the dashboard nginx:
//     `[edge proxy(es)] → dashboard nginx → api`. In ECS Express
//     production the dashboard is static S3+CloudFront (NOT in the api path),
//     so the api sits directly behind the shared ALB: `clients → CloudFront /
//     ECS Express ALB → api` — no dashboard-nginx hop, so prod leans on the
//     rightmost X-Forwarded-For token the ALB appends (X-Real-IP isn't set there).
//
//     Multi-hop chains are normalised at the dashboard-nginx layer via the
//     real_ip module (`set_real_ip_from` + `real_ip_recursive` in
//     services/dashboard/nginx.conf), which rewrites `$remote_addr` and
//     the propagated `X-Real-IP` to the first non-trusted XFF token — the
//     true client — before this code ever runs. So the rightmost-token
//     fallback here only fires on the rare direct-to-api path (tests,
//     `make start-dev`'s Vite proxy hitting :8080).
//  3. RemoteAddr — for direct-to-API requests (tests, dev mode).
//
// This is the single seam every package uses (auth rate-limiter, audit log,
// SSO callback session row). Duplicating the logic per-caller previously
// caused regressions: the auth package was fixed but audit and sso kept the
// broken leftmost-token form.
package httpip

import (
	"net"
	"net/http"
	"strings"
)

// Request returns the client IP. Returns nil when no header or RemoteAddr
// yields a parseable IP — callers persist that as NULL (audit_log.ip_address,
// sessions.ip) rather than fabricating a value.
func Request(r *http.Request) net.IP {
	if v := r.Header.Get("X-Real-IP"); v != "" {
		if ip := net.ParseIP(strings.TrimSpace(v)); ip != nil {
			return ip
		}
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.LastIndexByte(v, ','); i >= 0 {
			v = v[i+1:]
		}
		if ip := net.ParseIP(strings.TrimSpace(v)); ip != nil {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

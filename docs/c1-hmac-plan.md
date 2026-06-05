# C-1 — Ingestion HMAC plan

**Audit finding:** [`docs/security-audit-2026-05-09.md` §C-1](../docs/security-audit-2026-05-09.md)
**Tracking issue:** [#96](https://gitlab.com/axiaops/axiaops/-/issues/96)
**Branch:** `security/c1-ingestion-hmac` off `develop` at `dba1b68`
**Status:** design (this doc, post-review revision) → implementation pending
**Revision history:** v1 architect pass; v2 review pass — 5 substantive fixes + 6 smaller corrections + operator runbook section. See §13 for the change-log.

## Context

The api → ingestion call hop has no wire-level authentication today. Two endpoints on the ingestion binary accept any caller that can reach `axiaops-ingestion-${env}:8081` over the per-env docker network:

- `POST /scan` — `services/ingestion/cmd/main.go:144-206`. Body `{account_id, organization_id}` is trusted verbatim; the handler sets `storage.WithOrganizationID(ctx, req.OrganizationID)` on line 193 and runs the scan against whatever org the caller named.
- `POST /v1/credentials/verify` — `services/ingestion/cmd/main.go:210` → handler in `services/ingestion/cmd/verify.go`. Synchronous `sts:AssumeRole` probe; the response carries the resolved AWS account number on success.

The two api-side callers that hit those endpoints today:

- `services/shared/queue/sync/sync.go:46-50` — sync HTTP fallback used when `REDIS_URL` is unset (default in `start-dev` and any single-host deploy without Redis).
- `services/shared/queue/redis/redis.go:45-50` — Redis-backed enqueue. Note: the worker on `services/ingestion/cmd/worker.go:90-92` calls `runScan` **in-process** after `Dequeue` — there is no HTTP hop on the Redis path. The signing surface for the Redis flow is the queue *envelope* (LPUSH payload), not an HTTP request. See §4.4.
- `services/api/internal/api/account_role.go:119-156` — `verifyRoleViaIngestion` calls `POST /v1/credentials/verify` from the api side.

The doc/code disagreement (audit I-1): `services/ingestion/CLAUDE.md` "Endpoints" table advertises `POST /scan` as `Auth: Yes`. Both the audit (I-1) and issue #96's "Docs" checklist flag this; the same MR that closes C-1 closes I-1.

## Threat model

### What this fixes

- An attacker who can speak to `axiaops-ingestion-${env}:8081` (any container on the same docker network — including a future sidecar, a compromised komodo-periphery/portainer_agent on dev-1/dev-2, or any process that gains a pivot inside the network) **without** the shared secret can no longer:
  - Trigger arbitrary scans for any `organization_id` (CPU/AWS-quota burn, dashboard skew, scan running under a forged RLS context).
  - Pass arbitrary role ARNs to `/v1/credentials/verify` to enumerate AWS account numbers.
  - Drive `runScan` to decrypt and use any customer's AWS access keys (the audit's worst-case path: load any `accounts` row, decrypt under the shared `ENCRYPTION_KEY`, fire AWS API calls in that customer's account).
- Replay protection: a captured request body cannot be re-fired more than `MaxClockSkew` seconds later (§3.3).
- Defence-in-depth against an attacker landing on the Redis queue: §4.4 extends signing to the queue envelope so an LPUSH from a malicious actor cannot forge a scan job either.

### What this does NOT fix

- A leaked `INGESTION_SHARED_SECRET` is game-over. Anyone with the secret can call `/scan` for any org. The trust boundary shifts from "any container on the LAN" to "any process with the secret" — strictly smaller, but still operator-discipline-dependent. Mitigations: rotate (§5), File-type CI variables, never logged in full.
- User-level authorisation. This is service-to-service authentication ("is this caller part of our deployment?"). User-level authz ("did this user have permission to scan this account?") is enforced at the prior hop in api (`authz.PermAccountsScan` + audit_log write). Ingestion trusts that gate.
- Confidentiality on the wire. Bodies are still cleartext. Today's threat model is intra-host docker bridge (no plausible passive observer); when that changes, mTLS is the upgrade path (see §10).

## Protocol

### Signed material

**Option A — body only.** Signature = HMAC-SHA256(secret, raw body).
- Pro: simplest possible scheme.
- Con: a captured signed body replays forever (no timestamp).
- Con: a future GET endpoint would have no body and so no signature.

**Option B — body + timestamp.** Signature = HMAC-SHA256(secret, timestamp || "\n" || raw body).
- Pro: bounded replay window.
- Pro: composes cleanly with the verifier; receiver verifies timestamp first (cheap reject path), then HMAC.
- Con: still doesn't bind a signature to its path — a sig minted for `/scan` could in principle be re-used on a future endpoint that happens to accept the same body shape.

**Option C — body + timestamp + path.** Signature = HMAC-SHA256(secret, timestamp || "\n" || METHOD || "\n" || PATH || "\n" || raw body).
- Pro: binds signature to its target endpoint. Future endpoint additions are safe by construction.
- Pro: matches the SigV4-shaped canonical-request pattern AxiaOps engineers already know from AWS.
- Con: slightly more boilerplate on both sides; one extra failure mode in tests.

**Recommendation: C.** The marginal cost of binding `METHOD + PATH` is trivial (two `WriteString` calls inside the same `hmac.New` write loop) and it future-proofs the scheme against the failure mode "we added a new ingestion endpoint and someone forgot it inherits the same secret". The receiver canonicalises `r.Method` and `r.URL.Path` — *not* `r.URL.RequestURI()` — because query strings on these endpoints carry no semantic meaning today, and excluding them avoids surprises if a caller adds a debug query param.

**Canonical string format** (single seam, defined once in `shared/httpauth`):

```
canonical := unix_ts_seconds + "\n" + METHOD + "\n" + PATH + "\n" + body
sig       := base64.StdEncoding.EncodeToString(HMAC_SHA256(secret_bytes, canonical))
```

`base64.StdEncoding` (not RawURLEncoding) matches the AWS SigV4 ecosystem and produces deterministic-length strings (44 bytes for SHA-256). Body is signed as the exact bytes that go on the wire — the verifier must read the body into a buffer, hash it, then re-present it to the inner handler (see §4.2).

### Header layout

**Option A — three flat headers:**
```
X-AxiaOps-Timestamp: 1715740000
X-AxiaOps-Signature: c2lnLi4u
```

**Option B — SigV4-style Authorization:**
```
Authorization: AxiaOps-HMAC-SHA256 Credential=ingestion, Timestamp=1715740000, Signature=c2lnLi4u
```

**Option C — single combined header:**
```
X-AxiaOps-Ingestion-Token: ts=1715740000;sig=c2lnLi4u
```

**Recommendation: A.** Reasoning:
- Issue #96's acceptance criteria explicitly names `X-AxiaOps-Ingestion-Token` as the header. That was written before this design pass, but it telegraphs the team's preference for a self-documenting custom header over `Authorization:`.
- `Authorization:` is RFC 7235's scheme/credentials slot and conflates "user authentication" with "service-to-service signing" — confusing when audit logs already use `Authorization:` elsewhere for the dashboard session.
- Two headers (signature + timestamp) read cleaner than parsing a structured single-value header, and survive intermediaries that fold or canonicalise header values. The cost is one extra `req.Header.Get` per verify — negligible.

**Concrete names:**
```
X-AxiaOps-Ingestion-Timestamp: <unix seconds>
X-AxiaOps-Ingestion-Signature: <base64 HMAC-SHA256>
```

The `Ingestion-` infix is deliberate: any future shared-secret HMAC on a different hop (api → SaaS bridge, ingestion → AWS, etc.) gets its own header pair (`X-AxiaOps-Billing-Signature`, etc.) so the same package can serve many hops without overloading one name.

Issue #96 names the header `X-AxiaOps-Ingestion-Token` (singular). I'm proposing two split headers instead because the canonical string includes the timestamp — sticking it in a separate header is honest about what's being verified. The MR description should note this minor divergence from the issue text and ask for sign-off; if pushback comes, fall back to a single `X-AxiaOps-Ingestion-Token: ts=...;sig=...` header parsed in the same helper.

### Algorithm and key shape

**Algorithm: HMAC-SHA256.** Single option; everything else in this codebase that hashes uses SHA-256 (session ID hashing, license signatures move to RS256-of-SHA-256, AES-256-GCM for at-rest). No version negotiation knob — if we ever need to rotate the algorithm itself, a new env-var name (`INGESTION_SHARED_SECRET_V2`) is the cleaner story than versioned headers.

**Key shape: 32-byte hex.** Matches `ENCRYPTION_KEY` exactly:
- Env var carries 64 hex characters.
- Parsed once at startup via `hex.DecodeString` to a `[]byte` of length 32.
- 32 bytes = 256 bits = sufficient entropy for HMAC-SHA256 (the spec recommends key ≥ block size = 64 bytes for SHA-256, but ≥ output size = 32 bytes is the operationally-accepted minimum and matches our other secrets).

Generation: `openssl rand -hex 32` (same instruction as `ENCRYPTION_KEY`). Documented inline in `services/api/CLAUDE.md` and `services/ingestion/CLAUDE.md`.

The choice of hex (vs base64 / raw bytes / passphrase derived) is purely "match ENCRYPTION_KEY's shape so operators have one rotation playbook." Hex is verbose but copy-paste-safe in CI variables and `.env` files.

### Replay window and clock skew

**Default `MaxClockSkew`: 300 seconds (5 minutes).**

Reasoning:
- All current callers run in the same docker bridge network on the same self-hosted host → wall clock is identical (the host clock backs all containers).
- Future cross-host shape (ECS Express's api → an in-VPC ingestion) introduces real NTP-grade skew. 5 minutes accommodates typical NTP drift, brief NTP outages, and the network handler's queue+process latency without being a meaningful replay window.
- Too tight (60s): every transient docker-host clock jitter — `apt unattended-upgrades` triggering an NTP step — fails legitimate scans.
- Too loose (1h): a captured request body becomes a forgable scan trigger for an hour after recording.

**Tunable, not a Go literal.** Per the user's `feedback_config_format_yaml.md` memory, exposed as env var `INGESTION_HMAC_MAX_SKEW_SECONDS` (default 300). Operators can dial down for paranoia or up for cross-region debugging. The package exposes `httpauth.DefaultMaxSkew = 5 * time.Minute` as a Go constant so the test suite can reference it.

**Clock-skew handling — both directions.** Reject `timestamp < now - MaxSkew` AND `timestamp > now + MaxSkew`. Future-dated stamps must also be rejected — they'd otherwise let an attacker mint a far-future signature once with a leaked secret and replay it after rotation (the rotation window protection in §5 cannot help if a future-timestamped sig is already in the wild).

### Constant-time compare

**`hmac.Equal` on the receiver.** Not `crypto/subtle.ConstantTimeCompare` directly — `hmac.Equal` is `subtle.ConstantTimeCompare` underneath but is the canonical idiom for HMAC verification and reads as intent at the call site.

One site: `httpauth.Verify` in the shared package. The base64-decoded signature is compared against the computed digest:

```go
computed := mac.Sum(nil)               // []byte of length 32
provided, err := base64.StdEncoding.DecodeString(headerValue)
if err != nil { return ErrMalformedSignature }
if !hmac.Equal(computed, provided) { return ErrSignatureMismatch }
```

The base64 decode happens before the compare so a malformed base64 string surfaces as a distinct error (`ErrMalformedSignature`) without leaking compare timing.

**Note on length mismatch:** `hmac.Equal` delegates to `subtle.ConstantTimeCompare`, which returns 0 *immediately* when input lengths differ — the constant-time guarantee is **within equal-length slices**, not across length mismatches. This is correct behaviour (a length difference is not a "guessed N of M bytes right" oracle), but means an attacker who probes with shorter/longer signature blobs gets a fast reject. Acceptable: the base64-decode step already returns a distinct `ErrMalformedSignature` for unparseable input, and a parseable-but-wrong-length sig falls into the same fast-reject path without leaking the correct length (always 32 bytes for SHA-256, publicly known).

Tests in §7 verify the function returns the expected sentinel error on length-mismatched inputs — see §7.6 for the rationale.

### Why no nonce

**Decision: timestamp-only, no nonce.**

Reasoning:
- A nonce gives strict single-use, but requires receiver-side state — Redis SET-with-TTL of seen nonces, keyed by the signature itself (or a nonce field). The TTL must be ≥ `MaxClockSkew` for the protection to be meaningful.
- AxiaOps does have Redis. But:
  - The Redis cache fails open by design (Audit I-4 documents this). A nonce store that fails open is meaningless — an attacker forces a Redis-error path and replays freely.
  - A nonce store that fails closed adds a hard dependency: a Redis outage kills every scan trigger and every credentials-verify call.
  - The threat the nonce solves — replay within a 5-minute window by an attacker who already captured a signed request body — requires the attacker to already have docker-network position. That's the same position the HMAC is closing off in the first place. The marginal protection from "5-minute single-use enforcement" is small.
- Timestamp + small skew window is the standard AWS SigV4 / Stripe / GitHub webhook posture. They all chose stateless replay protection over nonce stores at this scale.

**Reserve the option.** The header format leaves room for a future `X-AxiaOps-Ingestion-Nonce` without rewriting the canonical string — add it as a new line in the canonical encoding (between PATH and body) gated on a `httpauth.RequireNonce bool` field in the verifier. Not in MVP.

## Code structure

### New shared package: `services/shared/httpauth/`

`httpauth` is shared between api and ingestion modules. Both already import from `axiaops.io/shared` and the existing per-api-internal helpers (`auth.requestIP`, `auth.decodeJSON`) are explicitly model patterns to be **promoted to shared** when a second module needs them (audit H-4 / C-3 follow-ups). This is exactly that case — api signs, ingestion verifies, both call the same canonical-string encoder.

**Public API surface** (kept minimal — every export earns its place):

```go
// services/shared/httpauth/httpauth.go

// Default replay window. Both ends use the same default but the verifier
// accepts it as a parameter so tests can shrink it.
const DefaultMaxSkew = 5 * time.Minute

// Algorithm identifier surfaced in errors + metric labels.
const SignatureAlgorithm = "HMAC-SHA256"

// Sentinel errors so callers can branch on the failure mode (the ingestion
// middleware emits one Prometheus label per kind — see §6).
var (
    ErrMissingTimestamp   = errors.New("httpauth: missing timestamp header")
    ErrMissingSignature   = errors.New("httpauth: missing signature header")
    ErrMalformedTimestamp = errors.New("httpauth: malformed timestamp header")
    ErrMalformedSignature = errors.New("httpauth: malformed signature header")
    ErrTimestampSkew      = errors.New("httpauth: timestamp outside skew window")
    ErrSignatureMismatch  = errors.New("httpauth: signature mismatch")
)

// Sign returns the base64-encoded HMAC-SHA256 of the canonical string for
// (timestamp, method, path, body). Used by callers (api side).
func Sign(secret []byte, timestamp time.Time, method, path string, body []byte) string

// Verify checks an inbound request's signature against the supplied secret.
// Returns nil on success, one of the sentinel errors above on failure.
// `now` is injected so tests can drive deterministic skew assertions.
// Production callers pass time.Now.
func Verify(secret []byte, maxSkew time.Duration, now func() time.Time,
            timestampHeader, signatureHeader string,
            method, path string, body []byte) error

// Middleware returns an http.Handler that wraps `next`, verifying the
// signature on every request. Public paths in `allowed` (exact match) skip
// verification. On failure: writes a 401 with `writeUnauthorised(w, err)`
// and increments the failures counter.
func Middleware(secret []byte, maxSkew time.Duration, allowed map[string]struct{},
                next http.Handler) http.Handler
```

**Why these signatures:**
- `Sign` takes a `time.Time` (caller passes `time.Now()`), not a Unix int. Internal conversion to `strconv.FormatInt(t.Unix(), 10)`. Keeps the time-value type-safe at the seam.
- `Verify` takes `now func() time.Time` not `time.Time` directly so the package itself can have unit tests that don't `time.Sleep`. Production wiring: `httpauth.Middleware(...)` passes `time.Now` (no parentheses).
- `Verify` takes the headers as **strings**, not `*http.Request` — keeps the package usable from non-HTTP contexts (the Redis envelope path in §4.4 reuses `Sign`/`Verify` against a payload, not an `*http.Request`).
- `Middleware` is the wire layer: reads headers off `*http.Request`, calls `Verify`, reads the body and re-presents it via `io.NopCloser(bytes.NewReader(body))` so the inner handler can still `json.NewDecoder(r.Body).Decode`.
- `allowed map[string]struct{}` for O(1) public-path lookup. Set in §4.2.

**Body-read seam.** `Middleware` reads the entire body with `io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))` (matches the H-4 64 KiB cap and prevents an attacker from sending a multi-GB request to OOM the verifier before the signature check).

**Body-oversize detection.** `http.MaxBytesReader` does **not** automatically emit 413 — it surfaces a `*http.MaxBytesError` from the `Read` call. The middleware must detect this explicitly:

```go
body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
if err != nil {
    var mbe *http.MaxBytesError
    if errors.As(err, &mbe) {
        http.Error(w, `{"error":"request_body_too_large"}`, http.StatusRequestEntityTooLarge)
        return
    }
    http.Error(w, `{"error":"bad_request"}`, http.StatusBadRequest)
    return
}
```

This 413-vs-400 detection is exactly the open follow-up tracked in issue #94 (surfaced during the H-4 hardening work). The C-1 MR should solve it once for both H-4 callers and C-1 by promoting this detection block into a tiny helper `httpauth.ReadCappedBody(w, r, max) ([]byte, error)` and cross-linking the helper from `services/api/internal/httpjson` so audit H-4 callers can adopt the same seam. **The implementer MUST close out the #94 sub-item in the same MR or file an explicit follow-up — leaving the helper uncalled by httpjson silently re-opens the 413-detection gap.**

**Public types only.** No exposed structs (`Verifier{}`, `Signer{}`, …). The functional shape forces callers to thread the secret through composition rather than caching it in package-level state — matches the codebase's "composition root holds secrets, handlers don't reach into env" posture.

**File layout:**

```
services/shared/httpauth/
    httpauth.go           # Sign / Verify / sentinel errors / canonical encoder
    middleware.go         # Middleware + writeUnauthorised + body-read seam
    httpauth_test.go      # Sign+Verify round-trip + every failure mode
    middleware_test.go    # http.Handler integration + body re-presentation
```

### Ingestion middleware integration

**Location:** wrap the two protected handlers, **not** the whole mux. Two reasons:

1. `/health`, `/livez`, `/readyz`, `/metrics` must stay reachable from docker healthchecks and Prometheus scrapers that have no shared secret. The api middleware's `publicPath` allowlist pattern in `services/api/internal/middleware/auth.go:45-60` is the model.
2. Future ingestion endpoints (none today, but issue #96 anticipates them) should explicitly opt in to HMAC by routing through `httpauth.Middleware`, not opt out via an allow-list.

**Concrete shape in `services/ingestion/cmd/main.go`** (post-edit, around line 130–215):

```
mux := http.NewServeMux()
mux.HandleFunc("GET /health", ...)         // public, unchanged
mux.Handle("GET /metrics", observability.MetricsHandler())   // public, unchanged

// Protected handlers — wrap each one individually with the HMAC middleware.
//
// `httpauthMW := httpauth.Middleware(secret, maxSkew, nil, http.Handler(...))`
// is the per-handler wrap. nil for `allowed` because we're scoped to a
// single route already.
mux.Handle("POST /scan", httpauth.Middleware(secret, maxSkew, nil,
    http.HandlerFunc(scanHandler)))
mux.Handle("POST /v1/credentials/verify", httpauth.Middleware(secret, maxSkew, nil,
    http.HandlerFunc(handleVerifyCredentials)))
```

The current inlined anonymous function for `POST /scan` (main.go:144-206) gets extracted into a named `scanHandler` method so the `Handle` registration is clean. The licence scan-gate logic stays inside `scanHandler` (it's per-request runtime state, not auth) — the HMAC check runs before the licence check, so an unauthenticated caller cannot probe licence state via 403 vs 401 timing.

**Composition root wires the secret in main.go around line 110–130** (between `license.VerifyAtBoot` and `newStore`):

```
ingestionSecret := loadIngestionSharedSecret()    // helper defined below
// loadIngestionSharedSecret die()s outside DEV_MODE if INGESTION_SHARED_SECRET
// is empty. In DEV_MODE empty is allowed; returns nil-or-empty.
maxSkew := loadHMACMaxSkew()    // 300s default; env override; pure function
```

`loadIngestionSharedSecret` is a small new function in `services/ingestion/cmd/main.go`:

```
func loadIngestionSharedSecret() []byte {
    v := os.Getenv("INGESTION_SHARED_SECRET")
    if v == "" {
        if devModeEnabled() {
            slog.Warn("hmac: INGESTION_SHARED_SECRET empty — DEV_MODE bypasses HMAC verification on ingestion")
            return nil
        }
        die("hmac: INGESTION_SHARED_SECRET is required when DEV_MODE=false")
    }
    secret, err := hex.DecodeString(v)
    if err != nil {
        die("hmac: INGESTION_SHARED_SECRET must be hex (got " + strconv.Itoa(len(v)) + " chars)", "error", err)
    }
    if len(secret) < 32 {
        die("hmac: INGESTION_SHARED_SECRET must be ≥ 32 bytes (64 hex chars), got " + strconv.Itoa(len(secret)))
    }
    // Safe diagnostic: length + first/last 4 hex chars, NEVER the full secret.
    slog.Info("hmac: ingestion shared secret loaded",
        "bytes", len(secret),
        "fingerprint", v[:4] + "…" + v[len(v)-4:],
    )
    return secret
}
```

When `secret == nil` (DEV_MODE only), the wrapping logic in `main.go` switches to a passthrough — there's no need to instantiate the middleware:

```
protect := func(h http.Handler) http.Handler { return h }   // passthrough
if ingestionSecret != nil {
    protect = func(h http.Handler) http.Handler {
        return httpauth.Middleware(ingestionSecret, maxSkew, nil, h)
    }
}
mux.Handle("POST /scan", protect(http.HandlerFunc(scanHandler)))
mux.Handle("POST /v1/credentials/verify", protect(http.HandlerFunc(handleVerifyCredentials)))
```

This keeps DEV_MODE's posture explicit (one `slog.Warn` at boot, no per-request decision). Matches the api binary's DEV_MODE-bypasses-auth-middleware seam (`serverbuild.ComposeServer` lines 302-303).

### api-side: sync queue + Redis queue + verify caller

The api binary has **three** outbound call sites that need to sign:

#### Sync queue — `services/shared/queue/sync/sync.go`

The current `New(ingestionURL)` constructor (line 30) becomes `New(ingestionURL, secret []byte)`. The constructor stores `secret` on the `Queue` struct; `Enqueue` builds the body, computes `ts := time.Now()`, calls `httpauth.Sign(q.secret, ts, "POST", "/scan", body)`, sets both headers on the outbound `*http.Request`.

When `secret == nil` (DEV_MODE on api), the constructor skips header-setting. The receiver's middleware was constructed with `secret == nil` too (passthrough), so both ends agree.

`Enqueue` post-edit:
```go
func (q *Queue) Enqueue(ctx context.Context, job ScanJob) error {
    body, err := json.Marshal(map[string]string{
        "account_id":      job.AccountID,
        "organization_id": job.OrganizationID,
    })
    if err != nil { return err }
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.ingestionURL+"/scan", bytes.NewReader(body))
    if err != nil { return err }
    req.Header.Set("Content-Type", "application/json")
    if q.secret != nil {
        ts := time.Now()
        sig := httpauth.Sign(q.secret, ts, http.MethodPost, "/scan", body)
        req.Header.Set("X-AxiaOps-Ingestion-Timestamp", strconv.FormatInt(ts.Unix(), 10))
        req.Header.Set("X-AxiaOps-Ingestion-Signature", sig)
    }
    resp, err := q.client.Do(req)
    if err != nil { return err }
    defer func() { _ = resp.Body.Close() }()
    if resp.StatusCode >= 400 {
        return fmt.Errorf("queue: sync enqueue: ingestion returned %d", resp.StatusCode)
    }
    return nil
}
```

The string literals `"X-AxiaOps-Ingestion-Timestamp"` and `"X-AxiaOps-Ingestion-Signature"` are exported as constants from `httpauth` (`HeaderTimestamp`, `HeaderSignature`) so caller and receiver agree at compile time — drop the literals once the constants land. (Spelled-out in the punch list §9.)

#### Redis queue — `services/shared/queue/redis/redis.go`

The HTTP-hop signing pattern doesn't apply to the Redis path — `worker.go` consumes the dequeued job in-process. The signing surface is the **queue envelope itself**. See §4.4 for the envelope-signing decision; key files touched on the api side are `services/shared/queue/redis/redis.go` (`Enqueue` signs the JSON payload, attaches `signature` + `timestamp` siblings) and `services/shared/queue/queue.go` (the `ScanJob` shape grows two new fields; the public Queue interface is unchanged).

#### verify caller — `services/api/internal/api/account_role.go`

`verifyRoleViaIngestion` at line 119 builds the body, POSTs to `h.ingestionURL+"/v1/credentials/verify"`. Same pattern as sync queue: sign with the api binary's loaded secret, attach two headers.

Where does it get the secret? The `Handler` struct (lines 33 + 50-55 of `handler.go`) holds `ingestionURL`; it grows a sibling `ingestionSecret []byte` field. The constructor `api.New(store, queue.Queue)` adds a fluent `WithIngestionSecret([]byte) *Handler` (mirroring the existing `WithIngestionURL(url string) *Handler` at line 77). Composition root passes the loaded secret via that setter.

### Composition root wiring

Both binaries acquire `INGESTION_SHARED_SECRET` from the env at the same lifecycle point — alongside `ENCRYPTION_KEY`. Concrete file:line plan:

**`services/api/cmd/main.go`**:

- Line 103 today: `q := queue.New(os.Getenv("REDIS_URL"), ingestionURL)`. Change `queue.New` signature to accept the secret as a third argument:
  ```go
  secret := loadIngestionSharedSecret(devMode)   // declared in this file, near die()
  q := queue.New(os.Getenv("REDIS_URL"), ingestionURL, secret)
  ```
  `loadIngestionSharedSecret` is the same function the ingestion binary will have (duplicated — two binaries, one shape) **OR** moved to `httpauth` as `httpauth.LoadFromEnv(name string, allowEmpty bool) ([]byte, error)`. **Pick the latter** — it's the seam every future shared secret will want.
- Around line 226 (composition of `deps`): `deps.IngestionSecret = secret` — new field in `serverbuild.Deps`.

The `serverbuild.Deps` struct grows one field (alphabetically just after `Queue`):
```go
// IngestionSecret signs outbound api → ingestion HTTP calls (POST /scan
// from the sync queue fallback, POST /v1/credentials/verify from the
// role-based onboarding flow). nil iff Config.DevMode.
IngestionSecret []byte
```

Inside `ComposeServer` (line 232), the `api.New` chain extends:
```go
apiH := api.New(deps.Store, deps.Queue).
    WithPublicHost(cfg.PublicHost).
    WithIngestionSecret(deps.IngestionSecret)
```

**`services/ingestion/cmd/main.go`**:

- Insert `loadIngestionSharedSecret` and `loadHMACMaxSkew` calls between the licence-verify block (line 110-112) and `newStore` (line 114). Result: a `secret []byte` and `maxSkew time.Duration` in scope when the mux is built starting at line 131.
- Lines 144 + 210: switch from `mux.HandleFunc(...)` to `mux.Handle("POST /scan", protect(http.HandlerFunc(scanHandler)))` per §4.2.

**`services/shared/queue/queue.go`** + **`services/shared/queue/sync/sync.go`** + **`services/shared/queue/redis/redis.go`**: thread the secret through the constructor. The package-level `queue.New` signature becomes `queue.New(redisURL, ingestionURL string, secret []byte) Queue`. Adapters pass the secret to the chosen backend. See §9 for exact files.

### DEV_MODE handling

**Decision: DEV_MODE allows an empty `INGESTION_SHARED_SECRET`; both ends fall back to no-signing.**

Reasoning, weighing both options:

- **Option A — DEV_MODE allows empty:** matches the codebase's broader DEV_MODE posture (auth bypass, license bypass via fixture). `start-dev` works out of the box with no extra env knob. The trade is one more "DEV_MODE bypasses X" line in the matrix; docs already say DEV_MODE bypasses every other security layer.
- **Option B — required in all modes (matches the post-C-2 ENCRYPTION_KEY posture):** every developer must `openssl rand -hex 32` once and add to `services/{api,ingestion}/.env`. Symmetrical with ENCRYPTION_KEY (which IS load-bearing in DEV_MODE because account.secret_encrypted decryption uses it). Fail-loud on first `start-dev` after the change.

**Recommend A.** ENCRYPTION_KEY is required in DEV_MODE because DEV_MODE still encrypts/decrypts real AWS secrets when a developer connects an account — the crypto stays load-bearing. The HMAC has no analogous function in DEV_MODE: it's pure wire authentication, and DEV_MODE explicitly disables wire authentication everywhere else. Holding HMAC to a stricter standard than the entire native-auth chain is inconsistent. The `slog.Warn` at boot when secret is empty + DEV_MODE makes the posture impossible to miss.

The DEV_MODE bypass MUST go through `devModeEnabled()` per `services/{api,ingestion}/CLAUDE.md` "Build tags" — direct `os.Getenv("DEV_MODE")` reads are caught by the `test:lint:no-direct-devmode` CI job (`.gitlab-ci.yml:222-245`). `loadIngestionSharedSecret(devMode bool)` takes a bool so the seam check stays at the composition root.

**Mismatched-mode detection.** A real operational failure mode: api ships in production-mode (signing requests) while ingestion ships in DEV_MODE (HMAC bypassed). The system works, but defence-in-depth has silently regressed. To surface this:

- When ingestion is in DEV_MODE (passthrough) AND receives a request carrying an `X-AxiaOps-Ingestion-Signature` header, emit a one-time `slog.Warn("hmac: DEV_MODE bypassed signed request — production api talking to dev ingestion?", "remote", r.RemoteAddr)`. Use a `sync.Once` so the warning is loud-but-not-spammy.
- The passthrough wrap function (`protect := func(h http.Handler) http.Handler { return h }`) needs a tiny variant that performs the header-presence check; the warning lives there, not in the inner handler.

Without this seam, a misconfig is completely silent and only surfaces on the next audit pass.

## Error shapes and observability

### 401 body

Match the post-C-3 / native-auth pattern in `services/api/internal/auth/handler.go:1246-1251` (`writeAuthError`):

```json
{"error":"ingestion_unauthorised"}
```

Code string: `ingestion_unauthorised` (one word, underscore, lowercase). Avoids both:
- The British vs American spelling debate — the codebase already uses British ("unauthorised") in `middleware/auth_native.go:50` and the auth handler. Stay consistent.
- Echoing the failure mode (missing vs wrong vs stale). The caller learns `401`; they don't learn whether they're missing a header or sent a stale timestamp. Internal observability (Prometheus + slog) captures the reason; the wire response stays opaque to avoid handing an attacker an oracle for "you're missing the secret entirely" vs "your clock is off."

Concrete response:
```http
HTTP/1.1 401 Unauthorized
Content-Type: application/json
WWW-Authenticate: AxiaOps-HMAC-SHA256

{"error":"ingestion_unauthorised"}
```

`WWW-Authenticate` is informational and harmless (it leaks the scheme, not the secret state).

### Log line shape

In `httpauth.Middleware` on failure, exactly one `slog.Warn` line:

```go
slog.Warn("hmac: request rejected",
    "method", r.Method,
    "path", r.URL.Path,
    "remote", r.RemoteAddr,
    "reason", reasonLabel,        // "missing_timestamp", "malformed_signature", etc.
    "ts_skew_seconds", skewSecs,  // signed delta, zero if not applicable
)
```

What is **explicitly never logged**:
- The shared secret bytes or hex form.
- The provided signature value.
- The provided timestamp value (the *skew* is logged; the raw value is unnecessary and would let an attacker correlate log lines if they ever land).
- The request body. (Bodies for `/scan` are trivial — `{account_id, organization_id}` — but the principle is "don't widen the audit-log blast radius without need.")

### Prometheus metric

One new counter, vector-labelled by failure reason. Mirrors `axiaops_session_revocations_total{reason="..."}`:

```go
axiaops_ingestion_hmac_failures_total{reason="missing_header"|"malformed"|"timestamp_skew"|"signature_mismatch"}
```

Labels (closed set):
- `missing_header` — neither timestamp nor signature present, OR one of them missing.
- `malformed` — timestamp not parseable as int, signature not parseable as base64.
- `timestamp_skew` — timestamp parsed cleanly but outside `[now-maxSkew, now+maxSkew]`.
- `signature_mismatch` — everything else valid, `hmac.Equal` returned false.

Four labels is small enough to keep `axiaops_ingestion_hmac_failures_total` low-cardinality (4 series total). Registered via the existing observability package — `services/shared/observability/` — alongside the auth-provider counters. Live in a new file `services/shared/observability/hmac.go` so it can be `Global.HMACFailures.WithLabelValues(reason).Inc()` from `httpauth.Middleware`.

**No success counter.** Every signed request is also an HTTP request, already counted by the existing `axiaops_http_requests_total{status="200"}` series. Adding `_hmac_verifications_total` doubles cardinality without new information.

**No timing histogram.** HMAC-SHA256 over a 1 KiB body is sub-microsecond; the existing request-duration histogram captures the verification cost as part of the request budget.

## Rotation strategy

The naive single-secret rotation has a guaranteed gap: ingestion ships with new secret → every api request 401s until api ships → restored. For typical AxiaOps deploy cadence (manual gates, per-host SSH-over-Docker push), that's a 30-second to several-minute hole per rotation. Not acceptable.

**Decision: two-secret support, current + previous.**

The ingestion verifier accepts a signature minted with either secret; the api signer always uses the current. Rotation sequence:

1. **Pre-rotation steady state:** both api and ingestion have `INGESTION_SHARED_SECRET = X`.
2. **Step 1 — stage new secret on ingestion:** set `INGESTION_SHARED_SECRET_NEXT = Y` on ingestion. Redeploy ingestion. Ingestion verifier now accepts X (current) **and** Y (next).
   - api still signs with X. All requests verify (matched current).
3. **Step 2 — flip api to new secret:** set `INGESTION_SHARED_SECRET = Y` on api. Redeploy api. api now signs with Y.
   - ingestion verifies Y against the `_NEXT` slot. All requests verify.
4. **Step 3 — promote on ingestion:** set `INGESTION_SHARED_SECRET = Y`, unset `INGESTION_SHARED_SECRET_NEXT`. Redeploy ingestion.
   - api signs Y, ingestion verifies against current. X is fully retired.
5. **Cleanup:** rotate the CI variable's history; document the rotation in the audit doc.

**Naming convention:** `INGESTION_SHARED_SECRET` (the primary) plus `INGESTION_SHARED_SECRET_NEXT` (optional staging slot). Ingestion's verifier walks both:

```go
// In ingestion main.go composition:
secrets := loadIngestionSecrets(devMode)   // returns [][]byte of length 1 or 2

// httpauth.Middleware takes a slice; on Verify it tries each in order and
// only returns failure if all of them fail. Signature: Verify still takes
// a single secret; the middleware loops.
mux.Handle("POST /scan",
    httpauth.MultiSecretMiddleware(secrets, maxSkew, nil, http.HandlerFunc(scanHandler)))
```

The `MultiSecretMiddleware` (new helper) is `Middleware` with the inner `Verify` call wrapped in a `for _, s := range secrets`. Constant-time iteration: it MUST try every secret even on first match, otherwise an attacker learns "I hit the current one" from timing. The simple loop with `var ok bool; for _, s := ... { if Verify(...) == nil { ok = true } }; if !ok { reject }` is sufficient — `hmac.Equal` is constant time per call, two calls is constant time over two calls.

The api side stays single-secret (`Sign` only needs the current one). No symmetry needed.

**Alternative considered: feature flag (`INGESTION_HMAC_ENFORCE=false` initially, flip to true after both sides deployed).**

- Pro: simpler initial rollout (no two-secret machinery from day 1).
- Con: makes the "enforce" state operator-controlled, which means an operator misclick re-opens the C-1 hole. Once enforced, the flag stays on forever — making it dead code we then have to remove. Worst of both worlds.

**Decision: skip the flag.** Use the two-secret machinery from day 1 for the initial rollout: deploy ingestion first with both `INGESTION_SHARED_SECRET` (X) and `INGESTION_SHARED_SECRET_NEXT` (also X — same value, defensive) configured. Then deploy api with X. The transition window has zero requests in flight if you sequence the manual gates correctly (issue user-memory: every deploy:* is a manual gate, so the operator clicks ingestion-then-api with a coffee in between). After api ships, set `INGESTION_SHARED_SECRET_NEXT=""` in CI and redeploy ingestion (the cleanup step from the rotation playbook above).

### Order of deployment

The first deploy with HMAC on must be:

1. **Ingestion first.** With `INGESTION_SHARED_SECRET = X` and `INGESTION_SHARED_SECRET_NEXT = X` (defensive duplicate; cleared in step 3).
   - Before this lands: ingestion accepts unauthenticated calls (today's behaviour).
   - After this lands: ingestion accepts unauthenticated calls **AND** signed calls. **Wait** — the verifier always requires a signature once configured. So a brief window will see api calls 401 if api hasn't shipped yet.
   - **Fix the order:** ship ingestion in "soft enforce" mode for the very first rollout — controlled by a `httpauth.SoftEnforce bool` field on the middleware. When true, missing or wrong signature logs the failure + increments the Prometheus counter but **does not return 401**. Lets us deploy ingestion first and observe legitimate api traffic (which will be unsigned during the transition) without breakage.

**Revised one-shot rollout:**

1. Ship ingestion with `INGESTION_SHARED_SECRET = X`, `INGESTION_HMAC_SOFT_ENFORCE = true`. Verify the Prometheus counter `axiaops_ingestion_hmac_failures_total{reason="missing_header"}` rises with every api call (api isn't signing yet). Observability check: count matches expected req/min, no surprises.
2. Ship api with `INGESTION_SHARED_SECRET = X`. Verify failures-counter drops to ~zero. Hold for one scan cycle (60 min by default) so scheduled scans flush through.
3. Flip ingestion to `INGESTION_HMAC_SOFT_ENFORCE = false`. Redeploy ingestion. Now wire is enforced.
4. (Optional cleanup) Remove the `_SOFT_ENFORCE` env var from `deploy/*.yml` after the first stable cycle — it's a transition-only knob.

**Env-var name:** `INGESTION_HMAC_SOFT_ENFORCE` (default `false`). Documented as a transition flag, expected to be removed in a follow-up MR after every env has been on hard-enforce for one full release cycle. The audit doc resolution-status block calls this out so it's not forgotten.

**Log volume in soft-enforce mode.** During the gap between ingestion-shipped-with-soft-enforce and api-shipped-with-signing, **every** api → ingestion call lands as unsigned. At scheduled-scan cadence (one job per account per N hours) × per-env account count, this is hundreds of `slog.Warn` lines per env. To avoid the operator runbook being drowned in expected-during-rollout noise:

- In soft-enforce mode the middleware emits per-request output at `slog.Debug`, not `slog.Warn`.
- A separate sampled `slog.Info("hmac: soft-enforce active", "missing_header_count_60s", N)` summary every 60s aggregates the count, via an `atomic.Int64` + a single goroutine ticker.
- Hard-enforce mode keeps per-request `slog.Warn` — those are real failures, not expected transitions.

**Detection that soft-enforce is stuck on.** If an operator forgets to flip `INGESTION_HMAC_SOFT_ENFORCE=false` after the rollout, C-1 silently re-opens (soft-enforce logs but never rejects). Two seams catch this:

1. New gauge `axiaops_ingestion_hmac_enforce_mode{mode="soft|hard"}` set once at boot. Prometheus alert: `axiaops_ingestion_hmac_enforce_mode{mode="soft"} == 1 AND env != "dev"` for > 24h.
2. The `axiaops_ingestion_hmac_failures_total{reason="missing_header"}` counter should drift to zero after api is shipped. If it stays > 0 / minute in a non-dev env, the rollout is incomplete (either soft-enforce-is-on or api is still unsigned in some calls — either way: not done).

Both signals belong in the operator runbook (§7).

Memory `feedback_deploys_always_manual.md` applies: every deploy is a manual click. The operator runbook for the rollout (added in §11) lists the exact button sequence per env.

### Per-env rollout order

Working from least-risk to most-risk (matches the C-2 rotation playbook):

1. `dev-1` then `dev-2` — DEV_MODE=true today, so the new code paths are exercised at composition-root level but the middleware is bypassed (§4.5). Mainly verifies the build is green and the env-var plumbing flows.
2. `preview` — auth-on per-MR env, ideal soak target.
3. `staging` — pre-release smoke.
4. `production` (ECS Express) — final.

Each env follows the three-step `SOFT_ENFORCE: true → false` sequence above. Total rollout calendar: ~1 week if observing one scan cycle (60 min) between steps in each env.

## Operator failure-mode guide

For the on-call who sees an alarm at 3am. Each row is "symptom → diagnostic → resolution."

| Symptom | api log signature | ingestion log signature | Likely cause | Resolution |
|---|---|---|---|---|
| Scans stuck in `scanning` status; user-clicked-Scan returns 200 but nothing happens | `scan: ingestion returned 401` or `queue: sync enqueue: ingestion returned 401` | `hmac: request rejected reason=signature_mismatch` | One side has rotated the secret; the other hasn't picked up. | Confirm both api and ingestion containers carry the same `INGESTION_SHARED_SECRET`. Redeploy the lagging side. If using two-secret rotation, verify `INGESTION_SHARED_SECRET_NEXT` on ingestion matches the new value on api. |
| Same symptom | Same | `hmac: request rejected reason=missing_header` | The api side hasn't been redeployed with HMAC code yet, OR `INGESTION_SHARED_SECRET` is empty on api (api silently shipped without signing). | Check `INGESTION_SHARED_SECRET` is set on the api container's env. Verify api binary was built from a commit that includes the C-1 HMAC code. |
| Same symptom | Same | `hmac: request rejected reason=timestamp_skew` | Clock drift between api host and ingestion host > `INGESTION_HMAC_MAX_SKEW_SECONDS` (default 300). | Check NTP sync on both hosts. Temporarily widen `INGESTION_HMAC_MAX_SKEW_SECONDS` only if NTP fix is in flight; never permanently. |
| Same symptom | `scan: ingestion returned 413` | (no ingestion log — request rejected before middleware) | Request body > 64 KiB. Not expected for `/scan` or `/verify`; suggests a malformed body. | Inspect the api-side log for the request body shape. Body cap is intentional (audit H-4). |
| Ingestion startup fails: `die: hmac: INGESTION_SHARED_SECRET is required when DEV_MODE=false` | n/a (container restart-loop) | n/a | `INGESTION_SHARED_SECRET` env var unset on ingestion in production / staging / preview. | Mint the secret in GitLab CI variables for the affected env scope; redeploy. |
| Soft-enforce period: massive log volume from ingestion at `slog.Info` (the summary counter) | api logs normal | `hmac: soft-enforce active missing_header_count_60s=N` (where N is large) | Expected during the ingestion-shipped-before-api window. Rollout step 1 → step 2 incomplete. | Complete step 2 (ship api with signing). Counter should drop to ~0 within one scan-cycle. |
| Hard-enforce period: `axiaops_ingestion_hmac_enforce_mode{mode="soft"} == 1` in production | n/a | n/a | Soft-enforce was never flipped off after rollout — C-1 is **silently re-opened**. | Set `INGESTION_HMAC_SOFT_ENFORCE=false` (or unset) and redeploy ingestion. File a runbook bug if this happens — the rollout cleanup MR for the env was missed. |
| `axiaops_ingestion_hmac_enforce_mode{mode="hard"} == 1` AND `axiaops_ingestion_hmac_failures_total > 0` per minute steady-state | Periodic `scan: ingestion returned 401` from one specific account | `hmac: request rejected reason=signature_mismatch` from one source | Single mis-configured caller (rare: an integration test pointing at a real ingestion, an unrotated CI variable on a specific scoped env). | Identify the caller via `remote` field in the ingestion log. Fix the misconfigured caller. |
| DEV_MODE ingestion receiving signed requests | api logs normal | `hmac: DEV_MODE bypassed signed request — production api talking to dev ingestion?` (once, via sync.Once) | api was deployed in production mode but `INGESTION_URL` points at a DEV_MODE-mode ingestion (e.g. local dev pointed at a remote test box). | Verify `INGESTION_URL` and DEV_MODE flags are aligned on both sides. |

**Standing dashboards to wire** (in addition to existing observability):

- `axiaops_ingestion_hmac_failures_total` — grouped by `reason`. Alert: > 1/min for > 5min in a hard-enforce env.
- `axiaops_ingestion_hmac_enforce_mode` — gauge {soft, hard}. Alert: `mode="soft"` in non-dev env for > 24h.
- `axiaops_ingestion_envelope_rejections_total` — Redis-path equivalent. Alert: > 1/min for > 5min.

## Test plan

### Unit tests — `services/shared/httpauth/`

**`httpauth_test.go`** (covers `Sign` + `Verify`):

| Case | Setup | Expected |
|------|-------|----------|
| 1.1 round-trip pass | Sign(secret, now, "POST", "/scan", body) → Verify(same args) | nil |
| 1.2 missing-timestamp-header | Verify(..., timestampHeader="") | ErrMissingTimestamp |
| 1.3 missing-signature-header | Verify(..., signatureHeader="") | ErrMissingSignature |
| 1.4 malformed-timestamp | Verify(..., timestampHeader="not-a-number") | ErrMalformedTimestamp |
| 1.5 malformed-signature | Verify(..., signatureHeader="not%base64") | ErrMalformedSignature |
| 1.6 stale-timestamp | now-(maxSkew+1s), valid sig over that ts | ErrTimestampSkew |
| 1.7 future-timestamp | now+(maxSkew+1s) | ErrTimestampSkew |
| 1.8 edge-just-inside-window | now-(maxSkew-1s) and now+(maxSkew-1s) | nil (both) |
| 1.9 edge-exactly-at-window | now-maxSkew exact | nil (inclusive at boundary — pin behaviour) |
| 1.10 wrong-secret | Sign with secretA, verify with secretB | ErrSignatureMismatch |
| 1.11 body-mutation-after-signing | Sign(body), then Verify(body+1 byte) | ErrSignatureMismatch |
| 1.12 method-mismatch | Sign for POST, Verify for PUT | ErrSignatureMismatch |
| 1.13 path-mismatch | Sign for /scan, Verify for /scan/extra | ErrSignatureMismatch |
| 1.14 empty-body | both signed and verified with body=nil | nil at the library layer (canonical encoding tolerates). **Note:** neither `/scan` nor `/v1/credentials/verify` accepts an empty body at the application layer — the inner handlers (via `httpjson.Decode`) 400 on empty input. This test pins library-level "supports empty body" so a future GET endpoint can sign successfully; it does not imply endpoint-level acceptance. |

**`middleware_test.go`** (covers `Middleware`):

| Case | Setup | Expected |
|------|-------|----------|
| 2.1 happy-path | Signed request through middleware | inner handler observes valid request; body readable via `r.Body` (not consumed by middleware) |
| 2.2 missing-header → 401 | No signature header | 401, body `{"error":"ingestion_unauthorised"}`, counter incremented `reason="missing_header"` |
| 2.3 wrong-sig → 401 | Wrong secret in caller | 401, counter `reason="signature_mismatch"` |
| 2.4 stale-ts → 401 | Old timestamp | 401, counter `reason="timestamp_skew"` |
| 2.5 body-cap-413 | Body > 64 KiB | 413 status; inner handler not invoked; counter NOT incremented (this is a separate failure mode) |
| 2.6 soft-enforce-pass-through | `SoftEnforce=true`, no headers | inner handler invoked; counter incremented `reason="missing_header"`; status from inner handler (not 401) |
| 2.7 inner-handler-can-decode-json | Signed request with JSON body | inner handler's `json.NewDecoder(r.Body).Decode(&v)` succeeds; verifies the body-re-presentation seam |

### Ingestion middleware integration — `services/ingestion/cmd/scan_handler_test.go`

New file. Tests:

- 3.1: `POST /scan` with no signature → 401 BEFORE the DB lookup (mock store records zero calls).
- 3.2: `POST /scan` with valid signature → store sees the GetAccount call.
- 3.3: `POST /v1/credentials/verify` with no signature → 401 BEFORE STS call (stub STS records zero calls — mirrors existing `verify_test.go` pattern).
- 3.4: DEV_MODE on (no secret loaded) — unsigned request reaches the inner handler. Exercises the passthrough path.

### Sync queue — `services/shared/queue/sync/sync_test.go`

New file (none exists today; the package has only `queue_test.go` at the parent level).

- 4.1 round-trip: `httptest.NewServer` mock receiver. Caller's `Enqueue` is followed by an assertion that the server saw both `X-AxiaOps-Ingestion-Timestamp` and `X-AxiaOps-Ingestion-Signature` headers, and that the signature verifies against the caller's secret.
- 4.2 nil-secret (DEV_MODE): mock receiver records that no auth headers were sent. Documents the explicit DEV_MODE bypass.

### Redis queue — `services/shared/queue/redis/redis_test.go` and `services/shared/queue/queue_test.go`

The Redis queue's signing surface is the envelope; details in §4.4. Test cases:

- 5.1 envelope-signed: `Enqueue(job)` writes a JSON payload to Redis whose `signature` field verifies against the caller's secret over `{org_id, account_id, enqueued_at, request_id}`.
- 5.2 worker-rejects-bad-envelope: `Dequeue` returns a payload whose signature doesn't verify → worker logs `worker: scan.rejected_invalid_envelope` and skips; Prometheus counter `axiaops_ingestion_envelope_rejections_total` incremented. (Counter shape — same as HMAC failures.)
- 5.3 worker-accepts-valid-envelope: full round-trip.

The existing `queue_test.go` suite needs a minor update: `var testJob = queue.ScanJob{...}` gets two new fields (`Signature`, `Timestamp`) populated. The shared `suite(t, q queue.Queue)` helper signs the job before enqueue. See §4.4.

### Integration test — `test-infra/integration/`

`docker-compose.test.yml` provides the existing stack. The new integration test:

- 6.1 round-trip-pass: `POST /v1/accounts/{id}/scan` from the api side completes; the ingestion side's scan handler is reached; assertions on `axiaops_ingestion_records_fetched_total` advancing.
- 6.2 mismatched-secrets: integration-compose override sets api's `INGESTION_SHARED_SECRET` to value A and ingestion's to value B. `POST /v1/accounts/{id}/scan` returns a 200 from the api (the scan is async-fired) but the underlying call fails. **Detection seam**: the api logs `scan.failed_to_trigger` from `services/ingestion/cmd/main.go:826` and `axiaops_ingestion_hmac_failures_total{reason="signature_mismatch"}` advances by 1.

The compose-override needs a new env-var injection point — easiest is to mint two distinct secrets in `test-infra/integration/docker-compose.test.yml`, one keyed `INGESTION_SHARED_SECRET_API` and one `INGESTION_SHARED_SECRET_INGESTION`, with the api container reading the first and the ingestion container reading the second. The test driver flips them to identical for the pass case and divergent for the fail case.

### Constant-time compare verification

The classical "measure two compares and check the duration is identical" test is flaky on shared CI runners. Two safer approaches:

**Option A — depend on `hmac.Equal`'s contract.** `hmac.Equal` is in the Go stdlib's `crypto/hmac` package; its godoc is the contract. Calling it (as opposed to `bytes.Equal`) is the audit trail. Test 1.10/1.11 above exercise the function; the constant-time property is taken as a stdlib guarantee.

**Option B — length-mismatch test.** Construct a wrong-length signature byte slice (say, 31 bytes instead of 32) and verify `Verify` returns `ErrSignatureMismatch` (not a panic, not a different error). This proves the comparator doesn't short-circuit on length difference. Code:

```go
func TestVerify_LengthMismatchNoShortCircuit(t *testing.T) {
    secret := []byte("...32 bytes...")
    body := []byte("{}")
    badSig := base64.StdEncoding.EncodeToString(make([]byte, 31))  // wrong length
    ts := time.Now()
    err := httpauth.Verify(secret, time.Minute, time.Now,
        strconv.FormatInt(ts.Unix(), 10), badSig,
        "POST", "/scan", body)
    if !errors.Is(err, httpauth.ErrSignatureMismatch) {
        t.Fatalf("expected ErrSignatureMismatch, got %v", err)
    }
}
```

**Recommend Option A + Option B together.** A is the canonical defence ("we used the stdlib's constant-time primitive"); B is the no-cost belt-and-braces that catches a future refactor swapping `hmac.Equal` for `bytes.Equal`.

## Redis queue envelope-signing decision (§4.4)

### Threat

Today's Redis path (`services/shared/queue/redis/redis.go`, `services/ingestion/cmd/worker.go`) does not cross any HTTP hop. The api `Enqueue` does an `LPUSH axiaops:scan_queue <json>`, the worker `BRPOP`s and calls `runScan` in-process. The HTTP-level HMAC closes the api→ingestion HTTP surface but not the Redis surface — anyone who can `LPUSH` directly to `axiaops:scan_queue` forges a scan job.

How realistic is that today?
- Redis is on the same docker network as api+ingestion. A compromised sidecar with the network position to hit ingestion:8081 has the same position to hit redis:6379.
- Redis is not password-authenticated in the current `deploy/*.yml` (search for `requirepass` — absent). Any process on `axiaops-${env}-network` can connect.
- A second `redis-cli LPUSH axiaops:scan_queue '{"organization_id":"victim-org-id","account_id":"victim-account-id"}'` from any container is a forged scan job that the worker happily executes.

The Redis-path threat surface is the same as the HTTP-path threat surface. Closing one and leaving the other open is half a fix.

### Recommendation: envelope-sign the queue payload

`queue.ScanJob` grows two new fields:

```go
type ScanJob struct {
    OrganizationID string    `json:"organization_id"`
    AccountID      string    `json:"account_id"`
    EnqueuedAt     time.Time `json:"enqueued_at"`
    RequestID      string    `json:"request_id"`
    Timestamp      int64     `json:"timestamp"`     // unix seconds, for skew check
    Signature      string    `json:"signature"`     // base64 HMAC-SHA256
}
```

What is signed: the canonical encoding of `{organization_id, account_id, enqueued_at, request_id, timestamp}` — i.e. every field EXCEPT `Signature` itself. The `httpauth` package needs a sibling helper:

```go
// SignEnvelope is the non-HTTP signing path: caller serialises the
// envelope-without-signature, calls SignEnvelope to compute the sig, then
// embeds it into the wire format. Receiver does the inverse:
// deserialises, blanks the Signature field, re-serialises, calls
// VerifyEnvelope.
//
// The "blank-then-resign" pattern is the same shape AWS uses for SQS message
// signing — robust against caller-side reordering of JSON keys because the
// serialisation is symmetric.
func SignEnvelope(secret []byte, payload []byte) string
func VerifyEnvelope(secret []byte, maxSkew time.Duration, now func() time.Time,
                     timestamp int64, payload []byte, signature string) error
```

`SignEnvelope` is the same canonical encoder as `Sign` but with method/path replaced by a fixed sentinel `"ENVELOPE\nQUEUE"` so an envelope signature cannot be reused as an HTTP signature and vice versa.

### Where it lives

- `services/shared/queue/redis/redis.go:45` — `Enqueue` signs the payload before LPUSH.
- `services/ingestion/cmd/worker.go` — verification happens **between `Dequeue` returning a job and any logging or DB write that echoes job fields.** Concretely: after the `if err != nil` block (lines 27–34) that handles the dequeue-error path, but **before** the existing `slog.Info("worker: scan.dequeued", ...)` block (lines 38–44) which today logs `account_id` / `organization_id` / `request_id` straight from the (still untrusted) envelope. Failure path on bad signature: emit a distinct log line that does NOT echo the attacker-claimed fields (`slog.Warn("worker: scan.rejected_invalid_envelope", "reason", ...)`), increment `axiaops_ingestion_envelope_rejections_total{reason}`, **do not count the failure toward the circuit breaker** (envelope failures are a categorically different class from scan-execution failures — a brief secret-mismatch during rotation must not open the breaker for legitimate scans afterwards), and `continue` the loop. This ordering matches the audit C-3 lesson: don't pollute logs with untrusted fields before verification.
- `services/shared/queue/queue.go` — `New(redisURL, ingestionURL string, secret []byte) Queue` threads the secret to both adapters.

### Effort impact

Without envelope signing: 1 hour (just the HTTP-path Redis-queue *header* changes go away — but you still need to thread the secret through the constructor for the sync path, so it's not free).

With envelope signing: +3 hours. Two new public functions in `httpauth`, one new struct field set across all three queue files, worker-side verification, two new tests (5.1, 5.2, 5.3 in §7).

**Recommend including envelope signing in this MR.** Skipping it leaves a known-exploitable Redis hole open, and the design is small enough (two helpers, mirror the HTTP-path pattern) that splitting it into a follow-up issue invites the follow-up to drift. The total MR remains under 1 week.

## Files to modify (punch list)

### Source — `services/shared/`

- [ ] `services/shared/httpauth/httpauth.go` (new) — `Sign`, `Verify`, `SignEnvelope`, `VerifyEnvelope`, sentinel errors, `HeaderTimestamp`/`HeaderSignature`/`DefaultMaxSkew`/`SignatureAlgorithm` constants, `LoadFromEnv` helper.
- [ ] `services/shared/httpauth/middleware.go` (new) — `Middleware`, `MultiSecretMiddleware`, `writeUnauthorised`, body-cap + body-re-presentation seam.
- [ ] `services/shared/httpauth/httpauth_test.go` (new) — §7.1 unit tests.
- [ ] `services/shared/httpauth/middleware_test.go` (new) — §7.2 middleware tests.
- [ ] `services/shared/observability/hmac.go` (new) — `Global.HMACFailures` Prometheus counter declaration + registration.
- [ ] `services/shared/queue/queue.go` — `ScanJob` gains `Timestamp int64` + `Signature string` fields; `New` signature gains `secret []byte`; adapters thread it through. Lines 16-21, 57-68. **⚠ Lockstep constraint:** the `queue.ScanJob` ↔ `redisqueue.ScanJob` ↔ `syncqueue.ScanJob` conversion at lines 34, 37–38, 46, 49–50 is **unkeyed type conversion** (`redisqueue.ScanJob(job)`), which requires all three structs to have **identical fields in identical order**. Adding `Timestamp` + `Signature` to only one or two will produce confusing compile errors at the conversion sites. As a defensive cleanup in the same MR, switch the conversions to keyed form (`redisqueue.ScanJob{OrganizationID: job.OrganizationID, ...}`) so future field additions surface as named-field errors instead of position errors.
- [ ] `services/shared/queue/sync/sync.go` — `New(ingestionURL, secret []byte)`; `Enqueue` signs the HTTP request when secret is non-nil. Lines 24-35, 37-60. **`ScanJob` struct must mirror `queue.ScanJob` and `redisqueue.ScanJob` exactly** — see lockstep note above.
- [ ] `services/shared/queue/redis/redis.go` — `New(redisURL, secret []byte)`; `Enqueue` envelope-signs before LPUSH; `Dequeue` returns the payload as-is (verification happens worker-side). Lines 24-42, 44-51. **`ScanJob` struct must mirror `queue.ScanJob` and `syncqueue.ScanJob` exactly** — see lockstep note above.
- [ ] `services/shared/queue/queue_test.go` — test fixture `testJob` gets `Timestamp`/`Signature` populated by signing in the suite helper.
- [ ] `services/shared/queue/sync/sync_test.go` (new) — §7.4 tests.
- [ ] `services/shared/queue/redis/redis_test.go` — extend with §7.5 cases (file already exists if `test:redis` CI job has artifacts; if not, new file).

### Source — `services/ingestion/`

- [ ] `services/ingestion/cmd/main.go` — insert `loadIngestionSharedSecret` + `loadHMACMaxSkew` between lines 112 and 114; extract the inline `POST /scan` handler at lines 144-206 into a named `scanHandler` func value; switch `mux.HandleFunc` to `mux.Handle` for `/scan` and `/v1/credentials/verify` wrapping each with `httpauth.Middleware` (lines 144, 210); pass `secret` to `queue.New` at line 225.
- [ ] `services/ingestion/cmd/scan_handler_test.go` (new) — §7.3 cases.
- [ ] `services/ingestion/cmd/worker.go` — between line 35 (`Dequeue` returns) and line 37 (`wait := ...`), insert `httpauth.VerifyEnvelope` on the job payload; on failure, log + `axiaops_ingestion_envelope_rejections_total{reason}` counter + continue.

### Source — `services/api/`

- [ ] `services/api/cmd/main.go` — load the secret near line 95 (between cache init and queue init); pass it to `queue.New` at line 103; thread it into `deps.IngestionSecret` near line 226.
- [ ] `services/api/internal/serverbuild/build.go` — add `IngestionSecret []byte` field to `Deps` struct (around line 100); call `apiH.WithIngestionSecret(deps.IngestionSecret)` near line 232.
- [ ] `services/api/internal/api/handler.go` — add `ingestionSecret []byte` field to `Handler` struct (line 33); add `WithIngestionSecret([]byte) *Handler` builder (after line 77).
- [ ] `services/api/internal/api/account_role.go` — in `verifyRoleViaIngestion` (line 119-156), sign the request between line 138 (`req.Header.Set("Content-Type", ...)`) and line 140 (`verifyHTTPClient.Do(req)`).
- [ ] `services/api/internal/api/account_role_test.go` — existing test (line 200's "unreachable port" test) — confirm it still passes; add a new test that asserts the outbound request carries both HMAC headers (use `httptest.NewServer` to intercept and read).

### Deploy plumbing

- [ ] `docker-compose.yml` — add `INGESTION_SHARED_SECRET: ${INGESTION_SHARED_SECRET:-}` to both ingestion (line 36+) and api (line 78+) environment blocks. Empty fallback is intentional for `make start-dev` — DEV_MODE bypasses HMAC. Document inline.
- [ ] `deploy/dev.yml` — add `INGESTION_SHARED_SECRET: ${INGESTION_SHARED_SECRET:-}` to ingestion (line 18+) and api (line 39+) blocks. Same fallback as docker-compose (dev-1/dev-2 are DEV_MODE=true).
- [ ] `deploy/preview.yml` — `INGESTION_SHARED_SECRET: ${INGESTION_SHARED_SECRET}` (bare, no fallback) for ingestion (line 20+) and api (line 40+). Preview is auth-on.
- [ ] `deploy/staging.yml` — `INGESTION_SHARED_SECRET: ${INGESTION_SHARED_SECRET}` (bare) for ingestion (line 13+) and api (line 33+).
- [ ] `deploy/demo.yml` — `INGESTION_SHARED_SECRET: ${INGESTION_SHARED_SECRET}` (bare) for ingestion (line 18+) and api (line 38+).
- [ ] `.gitlab-ci.yml` — line 596 (`.deploy-dev` template) and line 799 (`deploy:staging`): add `INGESTION_SHARED_SECRET="${INGESTION_SHARED_SECRET}"` to the env-prefix block on `docker-compose up`. Both `INGESTION_SHARED_SECRET` and (transition-only) `INGESTION_HMAC_SOFT_ENFORCE` propagated.
- [ ] GitLab CI variables (no file changes — UI / API) — mint `INGESTION_SHARED_SECRET` masked + raw + environment-scoped for `dev-1`, `dev-2`, `preview`, `staging`, `production`. Use `openssl rand -hex 32` per env (do NOT share across envs — each scoped variable gets its own value so a leak is bounded). Document the per-env minting in `docs/c1-hmac-plan.md` (this doc) operator runbook section.

### Test infrastructure

- [ ] `test-infra/integration/docker-compose.test.yml` — wire `INGESTION_SHARED_SECRET` from a `.env.test` file (or test-driver-injected). Provide a divergent-secrets compose override file for §7.6.2.

## Doc updates

- [ ] `services/ingestion/CLAUDE.md` (Endpoints table, line ~28): change `POST /scan` from `Auth: Yes` to `Auth: HMAC (X-AxiaOps-Ingestion-Signature)`. Add a new `POST /v1/credentials/verify` row with the same auth posture. Adds short explanatory paragraph: "service-to-service authentication via shared-secret HMAC; user-level authz is enforced upstream at the api hop."
- [ ] `services/ingestion/CLAUDE.md` (Environment Variables table, line ~140): add `INGESTION_SHARED_SECRET` row (Required: Yes outside DEV_MODE, no default, 32-byte hex). Add `INGESTION_HMAC_MAX_SKEW_SECONDS` row (No, default 300). Add `INGESTION_HMAC_SOFT_ENFORCE` row marked "transition-only — remove after first stable cycle per env."
- [ ] `services/api/CLAUDE.md` (Environment Variables table, line ~280): add `INGESTION_SHARED_SECRET` row (Required: Yes outside DEV_MODE, matches ingestion). Add `INGESTION_HMAC_MAX_SKEW_SECONDS` row.
- [ ] `services/shared/CLAUDE.md` (Package Map table): add `httpauth/` row — "shared-secret HMAC (HMAC-SHA256) for service-to-service auth. Today: api → ingestion. Reusable seam for future inter-service hops."
- [ ] `docs/security-audit-2026-05-09.md` — Resolution Status block at the top (the 2026-05-12 dated block referenced in the task description): flip C-1 from Open → Resolved with the MR ref. Also flip audit I-1 (doc drift) since the same MR fixes the CLAUDE.md `Auth: Yes` line.
- [ ] `docs/c1-hmac-plan.md` (this doc) — lives in the repo as the canonical design + operator runbook + rotation playbook.
- [ ] Repository-level `CLAUDE.md` (Source Control / Security sections) — no change (the security overview already says "audit-log + HMAC + RLS are the three trust seams"; adding HMAC's existence to the high-level summary is optional cleanup).

## Effort estimate

| Section | File count | Hours |
|---|---|---|
| `services/shared/httpauth/` (Sign + Verify + Middleware + 2 envelope helpers + tests) | 4 new files | 6 |
| `services/shared/observability/hmac.go` (single counter declaration + registration) | 1 new file | 0.5 |
| `services/shared/queue/` (queue.go + sync/sync.go + redis/redis.go + tests, including envelope-sign) | 3 modified, 1 new test file | 4 |
| `services/ingestion/cmd/` (main.go middleware integration + worker.go envelope verify + new test) | 2 modified, 1 new test file | 3 |
| `services/api/cmd/main.go` + `serverbuild/build.go` + `internal/api/handler.go` + `account_role.go` | 4 modified | 2.5 |
| `services/api/internal/api/account_role_test.go` (new HMAC header assertion test) | 1 modified | 1 |
| Deploy manifests (5 yml files + `.gitlab-ci.yml`) | 6 modified | 1.5 |
| GitLab CI variable minting (out-of-repo, UI/CLI work) | — | 1 |
| Doc updates (4 CLAUDE.md + audit + new plan doc — this doc) | 5 modified, 1 new | 2 |
| Integration test (`test-infra/integration/`) | 1 modified, 1 new override | 2.5 |
| MR review pass + fixup commits | — | 2 |
| Rollout — three-step soft-enforce → hard-enforce per env (×5 envs) including the 60-min observation window | — | 4 (calendar wall-time wins; engineer-attention ~2h) |
| **Total** | **~22 file changes** | **~35–40 engineer-hours, ~1 week calendar with rollout** |

**Estimate rationale (post-review):** raised from v1's ~30 hours to ~35–40 after factoring (a) the lockstep-`ScanJob`-conversion cleanup including switching to keyed conversion across all three packages, (b) the `httpauth.ReadCappedBody` helper and its retro-fit into `httpjson` per §4.1 / issue #94, (c) soft-enforce log-volume infra (atomic counter + ticker), (d) the mismatched-mode `sync.Once` warning, and (e) the operator runbook section (§7). Numbers below are unchanged where the section was already correctly estimated; deltas are highlighted in parentheses.

**Risk of balloon:**
- Envelope-signing for the Redis path (§4.4) is the biggest single risk if scope creeps — adding nonces or rejecting-on-Redis-outage would push it to 8+ hours. Stay disciplined: timestamp + base64 sig, no nonce, fail-open on Redis errors (matches the existing rate-limiter posture per audit I-4).
- The `INGESTION_HMAC_SOFT_ENFORCE` transition flag (§5) cleanup MR is filed as a follow-up issue and tracked, not done in this MR. If it's not filed, this work re-opens C-1 silently when someone later deletes the flag without checking the enforce posture.
- Integration-test compose plumbing for the divergent-secrets case is the second-biggest unknown — if the existing test-infra doesn't have a clean per-service env override seam, may add ~2 hours.

## Open questions / decisions deferred to MR

1. **Header naming — single `X-AxiaOps-Ingestion-Token: ts=...;sig=...` vs the proposed two-header `X-AxiaOps-Ingestion-Timestamp` + `X-AxiaOps-Ingestion-Signature` split.** Issue #96 names the single-header form. Design recommends split for clarity. The implementer should land the split and call this out in the MR description for sign-off; if pushback, the helper makes the swap trivial (one constant change).

2. **`INGESTION_HMAC_SOFT_ENFORCE` lifetime.** Design assumes one stable-cycle per env then remove. The cleanup MR is a follow-up issue; needs explicit `glab issue create` reference in the MR body to avoid drift. Confirm with the assignee: file the follow-up as part of this MR or as a precondition for closing C-1.

3. **DEV_MODE-allows-empty (§4.5) vs require-in-all-modes.** Design recommends the former; the user's `feedback_config_format_yaml.md` is silent on this trade-off. If the team's posture is "match ENCRYPTION_KEY's required-in-all-modes shape post-C-2 for consistency," that's a 30-line change to `loadIngestionSharedSecret` and a small `make start-dev` operator-runbook update. Cheap to flip; default to the design.

4. **Envelope signing on the Redis queue (§4.4 / §8).** The design strongly recommends including it. If the team wants to ship a smaller MR, file a follow-up issue immediately ("C-1.5: HMAC the Redis queue envelope") with the same canonical encoder seam — the worst outcome is two MRs both passing through `httpauth.SignEnvelope`. Confirm scope at MR-open.

5. **Secret rotation cadence.** The two-secret design supports zero-downtime rotation; the team should decide the cadence — annual minimum per the issue #96 threat-model note. The audit doc's "Cost-of-keys" section (if added later) can codify the cadence; for this MR, document the playbook in the operator runbook section of `docs/c1-hmac-plan.md` but don't pin a calendar.

6. **mTLS migration timeline.** Out of scope for this MR. Worth tagging an issue ("C-1 followup: replace shared-secret HMAC with mTLS when in-cluster cert-mgr lands") so the trade-off is recorded. The HMAC scheme designed here is the right cost/benefit point for the current single-host-per-env compose shape; mTLS is the right answer when ingestion moves off the same host as api.

7. **Redis `requirepass` follow-up.** The §4.4 / §8 envelope-signing recommendation rests on "Redis isn't password-authenticated, so any container on the docker network can LPUSH a forged job." A complementary defense-in-depth measure is to add `requirepass` (or ACL users) to Redis, requiring auth at the Redis-protocol level. This is **cheap** (a few lines in `deploy/*.yml`'s Redis service block + `REDIS_URL` carries the password via `redis://:<password>@host:6379`), **complementary** (HMAC stays load-bearing; Redis auth adds belt to the suspenders), and **independent** of this MR. File a follow-up issue ("harden: enable Redis requirepass across envs") and reference it from issue #94. Not in scope here.

8. **`X-AxiaOps-Ingestion-Timestamp` format.** Decision pinned: **Unix seconds**, big-endian integer, base-10 string. NOT milliseconds, NOT RFC 3339. Rationale: shortest wire form, no parsing ambiguity, matches AWS SigV4. A caller who sends milliseconds (e.g. `1715740000000`) will land far enough in the future to trip `ErrTimestampSkew` — that's a desirable failure mode (a buggy client cannot accidentally produce a valid signature). Worth pinning explicitly in the MR description so future SDK additions don't drift.

---

**File references** (every path here is verified by the architect against the working tree at `dba1b68`):

- `/Users/ahmed/Developer/repo/axiaops/docs/security-audit-2026-05-09.md` — C-1 source of truth.
- `/Users/ahmed/Developer/repo/axiaops/services/ingestion/cmd/main.go` — receiver mux registration (lines 130-215) + scan handler (144-206).
- `/Users/ahmed/Developer/repo/axiaops/services/ingestion/cmd/verify.go` — verify handler (lines 39-86).
- `/Users/ahmed/Developer/repo/axiaops/services/ingestion/cmd/worker.go` — Redis-path consumer (lines 19-126).
- `/Users/ahmed/Developer/repo/axiaops/services/shared/queue/queue.go` — `Queue` interface + `New` selector (lines 14-68).
- `/Users/ahmed/Developer/repo/axiaops/services/shared/queue/sync/sync.go` — sync HTTP enqueue (lines 30-60).
- `/Users/ahmed/Developer/repo/axiaops/services/shared/queue/redis/redis.go` — Redis LPUSH/BRPOP (lines 29-69).
- `/Users/ahmed/Developer/repo/axiaops/services/shared/queue/queue_test.go` — existing test fixture pattern.
- `/Users/ahmed/Developer/repo/axiaops/services/api/internal/api/account_role.go` — `verifyRoleViaIngestion` (lines 119-156).
- `/Users/ahmed/Developer/repo/axiaops/services/api/internal/api/handler.go` — `New` + `WithIngestionURL` (lines 50-78) — model for `WithIngestionSecret`.
- `/Users/ahmed/Developer/repo/axiaops/services/api/internal/auth/handler.go` — `decodeJSON` body-cap helper (line 1233), `requestIP` (line 1208), `writeAuthError` (line 1246).
- `/Users/ahmed/Developer/repo/axiaops/services/api/internal/middleware/auth.go` — `publicPath` allowlist pattern (lines 45-60).
- `/Users/ahmed/Developer/repo/axiaops/services/api/internal/middleware/auth_native.go` — `WrapNative` middleware structure (lines 32-71) — model for `httpauth.Middleware`.
- `/Users/ahmed/Developer/repo/axiaops/services/api/internal/serverbuild/build.go` — `Deps` struct (lines 92-143), `ComposeServer` (lines 207-345).
- `/Users/ahmed/Developer/repo/axiaops/services/api/cmd/main.go` — composition root for api binary (queue init at line 103; deps at lines 213-226).
- `/Users/ahmed/Developer/repo/axiaops/docker-compose.yml` — env-var wiring at lines 36-56 (ingestion) and 78-99 (api).
- `/Users/ahmed/Developer/repo/axiaops/deploy/dev.yml`, `preview.yml`, `staging.yml`, `demo.yml` — per-env compose files.
- `/Users/ahmed/Developer/repo/axiaops/.gitlab-ci.yml` — deploy templates at lines 544-633 (`.deploy-dev`) and 738-841 (`deploy:staging`); env-var propagation block at line 596 + 799.
- Issue text from `glab issue view 96` — canonical acceptance criteria (read at design time).

---

## §13 — Revision change-log

### v2 (review pass)

Five substantive fixes + six smaller corrections + three additions, all driven by an independent review of v1.

**Substantive fixes:**

1. **§9 (punch list) — `ScanJob` lockstep constraint flagged.** v1's punch list called for adding `Timestamp` + `Signature` fields to all three `ScanJob` structs (`queue`, `redis`, `sync`) but didn't note that they're related by unkeyed type conversion (`redisqueue.ScanJob(job)` at `services/shared/queue/queue.go:34`). A naive implementer adding fields to two of three would hit confusing compile errors at the conversion sites. v2 calls out the constraint and recommends switching to keyed conversion as a defensive cleanup in the same MR.
2. **§3.5 (constant-time compare) — `hmac.Equal` rationale corrected.** v1 claimed `hmac.Equal` "does the full constant-time compare even when lengths differ." Actually it short-circuits on length difference (via `subtle.ConstantTimeCompare`), which is correct behavior (length is not an oracle for "guessed N of M bytes") but means v1's stated justification was wrong. v2 explains the real semantics and reframes test 7.6's value as catching `bytes.Equal` regressions, not verifying constant-time-on-length-mismatch.
3. **§4.1 (body-read seam) — explicit 413 detection.** v1 said "Body > 64 KiB → 413" but `http.MaxBytesReader` doesn't auto-emit 413 — it surfaces `*http.MaxBytesError` on `Read`. The middleware needs explicit `errors.As(&http.MaxBytesError{})` detection. v2 pins this with code, promotes it into a `httpauth.ReadCappedBody` helper, and ties closure to issue #94's open 413-vs-400 sub-item so the same fix lands across H-4 callers in the same MR.
4. **§4.4 (worker integration) — log-ordering fix.** v1 said envelope-verify lives "between line 35 and 37" of worker.go. The actual `slog.Info("worker: scan.dequeued", ...)` at lines 38-44 already logs `account_id` / `organization_id` from the still-untrusted envelope before that point — matching the audit C-3 forensic-pollution shape. v2 reorders: verify first, log after, and on failure emit a distinct log line that does NOT echo attacker-claimed fields. Also explicit on circuit-breaker interaction (envelope failures do NOT count toward the breaker).
5. **§5 (rollout) — soft-enforce log volume + stuck-on detection.** v1 didn't address that soft-enforce mode produces hundreds of `slog.Warn` lines during the ingestion-before-api gap. v2 specifies: soft-enforce per-request output downgraded to `slog.Debug`, with a 60s `slog.Info` summary counter. v2 also adds an enforcement-mode gauge (`axiaops_ingestion_hmac_enforce_mode{mode}`) and Prometheus alert rule so a stuck-on `INGESTION_HMAC_SOFT_ENFORCE=true` doesn't silently re-open C-1.

**Smaller corrections:**

6. **§4.4 — worker.go line-number anchoring.** v1 referenced `line 35` and `line 37`; actual file has dequeue at line 26, the relevant gap is around 35-36. v2 replaces specific line numbers with structural anchors (`after the dequeue-error block, before the existing scan.dequeued log`) to survive minor refactors.
7. **§7 — test 1.14 empty-body clarification.** v1's "empty-body → nil (library tolerates)" test could be misread as endorsing empty bodies at the application layer. v2 clarifies: the inner handlers `400` on empty input via `httpjson.Decode`; the library-level pass exists so future GET endpoints can sign successfully.
8. **§4.5 — DEV_MODE / hard-enforce mismatch detection.** v1's DEV_MODE bypass is silent when api ships signed requests at a DEV_MODE ingestion. v2 adds a `sync.Once`-gated `slog.Warn("hmac: DEV_MODE bypassed signed request ...")` so the misconfig is loud-once, not silent-forever.
9. **§11 — effort estimate raised from ~30h to ~35–40h** after factoring the lockstep cleanup, the `ReadCappedBody` retrofit into httpjson, soft-enforce infra, mismatched-mode warning, and the operator runbook section.
10. **§3.5 — note on `hmac.Equal` length-mismatch fast-reject** explains why this is acceptable (length is public, not an oracle).
11. **§4.4 — explicit circuit-breaker non-interaction** for envelope-verify failures.

**Additions:**

- **§7 (Operator failure-mode guide) — new section.** Eleven symptom → diagnostic → resolution rows for the on-call. Covers steady-state failures, rotation drift, NTP drift, soft-enforce stuck-on, DEV_MODE misalignment. Three standing dashboard + alert specs.
- **§12 (open questions) — two new entries.** Q7: Redis `requirepass` follow-up (complementary defense-in-depth to envelope signing). Q8: Pin the timestamp wire format as Unix seconds (not milliseconds, not RFC 3339).

### v1 (architect pass)

Initial design produced via the architect agent, covering protocol, code seams, rotation strategy, rollout, test plan, files-to-modify, and six open questions.

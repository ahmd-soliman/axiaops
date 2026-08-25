package httpauth

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"axiaops.io/shared/observability"
)

// ReadCappedBody reads at most maxBytes from r.Body, returning the bytes plus
// any read error. The caller is expected to translate the error into the
// appropriate HTTP status — see WriteBodyReadError for the canonical mapping.
//
// Promoted out of the middleware so audit H-4 callers (services/api/internal
// /httpjson) can adopt the same 413-vs-400 detection seam.
func ReadCappedBody(w http.ResponseWriter, r *http.Request, maxBytes int64) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
}

// WriteBodyReadError writes a 413 if the underlying error is a body-cap
// overflow, otherwise a 400 generic bad-request. Returns true iff a response
// was written (always true when err != nil).
func WriteBodyReadError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var mbe *http.MaxBytesError
	w.Header().Set("Content-Type", "application/json")
	if errors.As(err, &mbe) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = w.Write([]byte(ResponseBodyTooLarge))
		return true
	}
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(ResponseBadRequest))
	return true
}

// Options bundles the knobs Middleware accepts. Held as a struct so future
// flags (RequireNonce, AllowedPaths) drop in without churning every caller.
type Options struct {
	// MaxSkew is the replay window. Zero falls back to DefaultMaxSkew.
	MaxSkew time.Duration

	// SoftEnforce, when true, makes the middleware log + count failures but
	// pass the request through to the inner handler regardless. Transition
	// flag used during the initial rollout: ship ingestion with
	// SoftEnforce=true so legitimate api traffic (which won't be signing
	// yet) doesn't 401 in the gap between ingestion-shipped-with-HMAC and
	// api-shipped-with-signing. Flip to false after one stable cycle.
	SoftEnforce bool

	// Now is the time source. Production wiring passes time.Now; tests
	// pass a fixed-time stub. Nil falls back to time.Now.
	Now func() time.Time
}

// Middleware returns an http.Handler that verifies the HMAC signature on
// every request, then re-presents the body to the inner handler. Pass nil
// secret for an explicit DEV_MODE passthrough — callers should prefer to
// skip the wrap entirely (see PassthroughWithWarning) so a one-shot
// startup log makes the posture obvious.
//
// Thin wrapper over MultiSecretMiddleware so the default-injection logic
// for opts.MaxSkew / opts.Now lives in exactly one place.
func Middleware(secret []byte, opts Options, next http.Handler) http.Handler {
	return MultiSecretMiddleware([][]byte{secret}, opts, next)
}

// MultiSecretMiddleware is Middleware but accepts a slice of accepted secrets
// (current + previous) for zero-downtime rotation.
// Verifies against each secret in order; iterates the full slice on success
// AND failure so timing cannot leak which slot matched.
//
// When secrets contains a single nil entry (i.e. DEV_MODE composition root)
// the middleware is a passthrough. Callers that want the one-shot warning on
// signed-request-into-passthrough should use PassthroughWithWarning instead.
func MultiSecretMiddleware(secrets [][]byte, opts Options, next http.Handler) http.Handler {
	maxSkew := opts.MaxSkew
	if maxSkew <= 0 {
		maxSkew = DefaultMaxSkew
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	// Filter out nil/empty secrets — they represent DEV_MODE passthrough.
	active := make([][]byte, 0, len(secrets))
	for _, s := range secrets {
		if len(s) > 0 {
			active = append(active, s)
		}
	}
	if len(active) == 0 {
		return next
	}

	// Soft-enforce log volume guard: when SoftEnforce is true, every
	// missing-header request emits a slog.Debug (not Warn) and a
	// once-per-minute slog.Info summary counter aggregates them.
	var softCounter atomic.Int64
	var softTickerOnce sync.Once

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := ReadCappedBody(w, r, MaxBodyBytes)
		if err != nil {
			WriteBodyReadError(w, err)
			return
		}

		tsHeader := r.Header.Get(HeaderTimestamp)
		sigHeader := r.Header.Get(HeaderSignature)

		// Try each secret. Constant-time iteration: do NOT early-return on
		// success — an attacker timing the response could otherwise tell
		// which slot matched.
		var lastErr error
		matched := false
		for _, s := range active {
			vErr := Verify(s, maxSkew, now, tsHeader, sigHeader, r.Method, r.URL.Path, body)
			if vErr == nil {
				matched = true
			} else {
				lastErr = vErr
			}
		}

		if matched {
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
			next.ServeHTTP(w, r)
			return
		}

		reason := reasonLabel(lastErr)
		observability.RecordHMACFailure(reason)

		if opts.SoftEnforce {
			// Lazy-start the per-minute summary ticker on first failure.
			softTickerOnce.Do(func() {
				go func() {
					t := time.NewTicker(time.Minute)
					defer t.Stop()
					for range t.C {
						n := softCounter.Swap(0)
						if n > 0 {
							slog.Info("hmac: soft-enforce active",
								"missing_header_count_60s", n,
							)
						}
					}
				}()
			})
			softCounter.Add(1)
			slog.Debug("hmac: soft-enforce request",
				"method", r.Method,
				"path", r.URL.Path,
				"reason", reason,
			)
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
			next.ServeHTTP(w, r)
			return
		}

		var skewSecs int64
		if errors.Is(lastErr, ErrTimestampSkew) {
			if tsInt, parseErr := parseInt64(tsHeader); parseErr == nil {
				skewSecs = now().Unix() - tsInt
			}
		}
		slog.Warn("hmac: request rejected",
			"method", r.Method,
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
			"reason", reason,
			"ts_skew_seconds", skewSecs,
		)
		writeUnauthorised(w)
	})
}

// PassthroughWithWarning wraps next without any HMAC verification, but emits
// a one-shot slog.Warn the first time it sees an inbound signed request.
// Composition roots wire this when they detected they are in DEV_MODE
// (secret unset) — it surfaces a misconfig where a signed api binary points
// at a DEV_MODE ingestion binary (defence-in-depth has silently regressed).
func PassthroughWithWarning(next http.Handler) http.Handler {
	var once sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(HeaderSignature) != "" || r.Header.Get(HeaderTimestamp) != "" {
			once.Do(func() {
				slog.Warn("hmac: DEV_MODE bypassed signed request — production api talking to dev ingestion?",
					"remote", r.RemoteAddr,
					"method", r.Method,
					"path", r.URL.Path,
				)
			})
		}
		next.ServeHTTP(w, r)
	})
}

// parseInt64 parses a positive or negative base-10 int64 from s. Used
// only for the diagnostic "ts_skew_seconds" log field — the canonical
// timestamp parse in Verify is the load-bearing one; this is best-effort
// telemetry.
func parseInt64(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

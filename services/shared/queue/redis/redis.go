// Package redis provides a Redis-backed implementation of queue.Queue.
// Jobs are enqueued with LPUSH and dequeued with BRPOP (blocking pop).
//
// The Redis path does not cross an HTTP hop — the ingestion worker
// consumes jobs in-process — so wire-level HMAC is meaningless here.
// The signing surface is the queue envelope itself: Enqueue computes a
// signature over the JSON payload (with the Signature field blanked)
// and embeds it back into the wire form. Dequeue returns the payload
// as-is; the worker calls httpauth.VerifyEnvelope before trusting any
// field on it.
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"axiaops.io/shared/httpauth"
)

const queueKey = "axiaops:scan_queue"

// ScanJob mirrors queue.ScanJob to avoid an import cycle. Timestamp +
// Signature are populated by Enqueue; consumers MUST call VerifyEnvelope
// before trusting any other field (see worker.go).
type ScanJob struct {
	OrganizationID string    `json:"organization_id"`
	AccountID      string    `json:"account_id"`
	EnqueuedAt     time.Time `json:"enqueued_at"`
	RequestID      string    `json:"request_id"`
	Timestamp      int64     `json:"timestamp,omitempty"`
	Signature      string    `json:"signature,omitempty"`
}

// Queue is a Redis-backed scan job queue.
type Queue struct {
	rdb    *redis.Client
	secret []byte
}

// New creates a Redis queue client from the given URL. secret is the shared
// HMAC secret loaded from INGESTION_SHARED_SECRET; pass nil in DEV_MODE.
//
// The redisURL string may carry a Redis ACL/requirepass credential:
//
//	redis://:${REDIS_PASSWORD}@host:6379
//
// redis.ParseURL handles the userinfo parse — no extra wiring needed.
func New(redisURL string, secret []byte) (*Queue, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	rdb := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return &Queue{rdb: rdb, secret: secret}, nil
}

// Enqueue signs the envelope and pushes the job to the left of the queue
// list. When secret is nil (DEV_MODE) the Timestamp + Signature fields are
// left as the caller supplied them (typically zero values).
//
// The signing protocol: serialise the job with Signature=""; HMAC the
// resulting bytes; store the base64 sig back into the struct; serialise
// again for the wire. The receiver inverts: deserialise → save Signature →
// blank Signature → reserialise → VerifyEnvelope. Symmetric serialisation
// is the key invariant; relying on encoding/json's deterministic field
// ordering keeps the pattern simple.
func (q *Queue) Enqueue(ctx context.Context, job ScanJob) error {
	if len(q.secret) > 0 {
		ts := time.Now()
		job.Timestamp = ts.Unix()
		job.Signature = ""
		payload, err := json.Marshal(job)
		if err != nil {
			return fmt.Errorf("queue: marshal job for signing: %w", err)
		}
		job.Signature = httpauth.SignEnvelope(q.secret, ts, payload)
	}
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("queue: marshal job: %w", err)
	}
	return q.rdb.LPush(ctx, queueKey, data).Err()
}

// Dequeue blocks until a job is available (BRPOP with no timeout). The
// returned ScanJob carries the Timestamp + Signature fields as they
// appeared on the wire; the worker MUST call httpauth.VerifyEnvelope
// before trusting any other field.
func (q *Queue) Dequeue(ctx context.Context) (ScanJob, error) {
	result, err := q.rdb.BRPop(ctx, 0, queueKey).Result()
	if err != nil {
		return ScanJob{}, fmt.Errorf("queue: dequeue: %w", err)
	}
	var job ScanJob
	if err := json.Unmarshal([]byte(result[1]), &job); err != nil {
		return ScanJob{}, fmt.Errorf("queue: unmarshal job: %w", err)
	}
	return job, nil
}

// Close closes the underlying Redis connection.
func (q *Queue) Close() error {
	return q.rdb.Close()
}

// VerifyEnvelope checks the envelope signature on a dequeued job against
// the supplied secret. It reconstructs the canonical payload the same way
// Enqueue did (blank the Signature, re-serialise) before delegating to
// httpauth.VerifyEnvelope.
//
// secret may be nil — in DEV_MODE both ends agree to no-sign and this
// helper returns nil for any input (including a missing signature). Once
// the operator configures INGESTION_SHARED_SECRET on ingestion, this
// function rejects unsigned jobs.
//
// Returns one of the httpauth.Err* sentinel errors on failure so the
// worker can label its metric (use httpauth.ReasonLabel via the sentinel).
func VerifyEnvelope(secret []byte, maxSkew time.Duration, now func() time.Time, job ScanJob) error {
	if len(secret) == 0 {
		return nil
	}
	sigOnWire := job.Signature
	ts := job.Timestamp
	if sigOnWire == "" {
		return httpauth.ErrMissingSignature
	}
	job.Signature = ""
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("queue: marshal job for verify: %w", err)
	}
	return httpauth.VerifyEnvelope(secret, maxSkew, now, ts, payload, sigOnWire)
}

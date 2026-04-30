package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"axiaops.io/shared/storage"
)

// Plan §4.5 / decision D5 — first-owner bootstrap install token.
//
// On startup, if no organizations exist:
//   1. The operator either supplies BOOTSTRAP_INSTALL_TOKEN, or we
//      generate a 32-byte random token.
//   2. We hash it (SHA-256) and persist the hash via Store.CreateBootstrapState
//      under a PG advisory lock so multi-replica deployments end up with
//      exactly one bootstrap row (architect C5).
//   3. We write the plaintext token to BOOTSTRAP_TOKEN_FILE_PATH (mode 0600),
//      and OPTIONALLY print a banner with the token to stdout (only when
//      BOOTSTRAP_PRINT_BANNER=true — default-secure per architect S8 so
//      log aggregators don't capture the token).
//   4. The /v1/auth/bootstrap handler accepts the plaintext token, the
//      Store.ConsumeBootstrapState method does a constant-time compare,
//      creates the org+user+membership+session in one tx, and deletes the
//      singleton — sealing the endpoint forever.
//
// If an organization already exists at startup, this is a no-op — the
// endpoint stays sealed and any leftover token file from a previous run
// is left untouched (the operator removes it as part of the install
// runbook).

const (
	envInstallToken     = "BOOTSTRAP_INSTALL_TOKEN"
	envTokenFilePath    = "BOOTSTRAP_TOKEN_FILE_PATH"
	envPrintBanner      = "BOOTSTRAP_PRINT_BANNER"
	defaultTokenFile    = "/var/run/axiaops/initial_setup_token"
	installTokenBytes   = 32
)

// InstallTokenResult describes what the generator did. Returned to the
// composition root mostly for logging / test assertions.
type InstallTokenResult struct {
	// Generated is true when this replica won the race and minted the
	// token. False when another replica won, or when bootstrap is
	// already complete (organizations already exist), or when an
	// organization already exists (the no-op path).
	Generated bool
	// Skipped is true when no token was minted because organizations
	// already exist. The endpoint stays sealed and no file is written.
	Skipped bool
	// FilePath is where the plaintext was written, or "" if file
	// writing was disabled (BOOTSTRAP_TOKEN_FILE_PATH set to "").
	FilePath string
	// HostName is the value of HOSTNAME at generation — written to
	// bootstrap_state.minted_by_pod for cluster-debug purposes.
	HostName string
}

// MaybeGenerateInstallToken inspects the bootstrap state and either
// generates+persists a token or returns Skipped=true. Idempotent: a
// second call after success is a no-op (the bootstrap row was deleted
// by the consume path, and organizations now exist).
//
// Concurrency: safe under multi-replica startup. Store.CreateBootstrapState
// uses pg_advisory_xact_lock + ON CONFLICT DO NOTHING so exactly one
// replica wins. Losers log a peer-victory line and continue.
func MaybeGenerateInstallToken(ctx context.Context, store storage.NativeAuthStore) (InstallTokenResult, error) {
	hostName, _ := os.Hostname()

	count, err := store.CountOrganizations(ctx)
	if err != nil {
		return InstallTokenResult{}, fmt.Errorf("auth: install token: count organizations: %w", err)
	}
	if count > 0 {
		// Bootstrap already complete on this install.
		return InstallTokenResult{Skipped: true, HostName: hostName}, nil
	}

	plaintext, err := installTokenPlaintext()
	if err != nil {
		return InstallTokenResult{}, fmt.Errorf("auth: install token: %w", err)
	}
	hash := HashToken(plaintext)

	won, err := store.CreateBootstrapState(ctx, hash, hostName)
	if err != nil {
		// ErrBootstrapAlreadyDone means an organization was created
		// between our CountOrganizations and the lock acquisition —
		// extraordinarily rare, but treat as Skipped for clarity.
		if errors.Is(err, storage.ErrBootstrapAlreadyDone) {
			return InstallTokenResult{Skipped: true, HostName: hostName}, nil
		}
		return InstallTokenResult{}, fmt.Errorf("auth: install token: create state: %w", err)
	}
	if !won {
		// Peer replica won the race. They're writing the file/banner;
		// we just log and move on. The (cluster-shared) PG state is
		// the source of truth, so any incoming bootstrap request
		// served by us validates against the row peer wrote.
		slog.Info("auth: install token already minted by peer replica", "hostname", hostName)
		return InstallTokenResult{HostName: hostName}, nil
	}

	res := InstallTokenResult{Generated: true, HostName: hostName}

	// Step 3: write the plaintext to a file (mode 0600). Empty-string
	// override (BOOTSTRAP_TOKEN_FILE_PATH="") disables file writing
	// entirely — useful for ephemeral / read-only container roots
	// (ECS Fargate, distroless) where stdout is the only durable
	// channel. Distinguish "set to empty" from "unset" via
	// os.LookupEnv so an unset env still falls through to the default.
	filePath := defaultTokenFile
	if v, ok := os.LookupEnv(envTokenFilePath); ok {
		filePath = strings.TrimSpace(v)
	}
	if filePath != "" {
		if err := writeTokenFile(filePath, plaintext); err != nil {
			slog.Warn("auth: install token: file write failed",
				"err", err, "path", filePath)
		} else {
			res.FilePath = filePath
		}
	}

	// Step 4: optionally print the banner. Default-secure: when the env
	// var is unset or false, we log only the file path — never the
	// token value (architect S8). When BOOTSTRAP_INSTALL_TOKEN was
	// supplied by the operator they already know it; suppress the
	// banner regardless of BOOTSTRAP_PRINT_BANNER.
	suppress := os.Getenv(envInstallToken) != ""
	if !suppress && os.Getenv(envPrintBanner) == "true" {
		printInstallBanner(plaintext, res.FilePath)
	} else if !suppress {
		path := res.FilePath
		if path == "" {
			path = "(file write disabled)"
		}
		slog.Info("auth: install token written; cat the file to retrieve it",
			"path", path, "hostname", hostName)
	}

	return res, nil
}

func installTokenPlaintext() (string, error) {
	// BOOTSTRAP_INSTALL_TOKEN is the operator-supplied override for
	// unattended installs (k8s operator, helm hook, etc). When set,
	// we use it verbatim — same single-use semantics as the auto-
	// generated form. Operators are expected to clear this env var
	// from secret stores after first boot.
	if v := strings.TrimSpace(os.Getenv(envInstallToken)); v != "" {
		return v, nil
	}
	buf := make([]byte, installTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read entropy: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// writeTokenFile writes the plaintext atomically with mode 0600. Atomic =
// write to <path>.tmp then rename. Avoids leaving a partially-written
// file if the process dies mid-write.
func writeTokenFile(path, plaintext string) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(plaintext), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// removeInstallTokenFile deletes the install-token file post-bootstrap
// (plan §4.6 AC2). Honours the same env conventions as
// MaybeGenerateInstallToken: BOOTSTRAP_TOKEN_FILE_PATH unset → default
// path; explicitly empty → file management disabled (no-op);
// non-empty → that path. Best-effort: missing file is fine, other
// errors are logged but never fail the bootstrap response.
func removeInstallTokenFile() {
	filePath := defaultTokenFile
	if v, ok := os.LookupEnv(envTokenFilePath); ok {
		filePath = strings.TrimSpace(v)
	}
	if filePath == "" {
		return
	}
	if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("auth: install token file removal failed",
			"err", err, "path", filePath)
	}
}

func printInstallBanner(token, filePath string) {
	pathLine := "(file write disabled)"
	if filePath != "" {
		pathLine = filePath
	}
	banner := fmt.Sprintf(`
╔══════════════════════════════════════════════════════════════════╗
║  AxiaOps first-run setup                                         ║
║                                                                  ║
║  Visit:   https://<your-host>/bootstrap                          ║
║  Token:   %s
║                                                                  ║
║  This token is single-use and grants creation of the first       ║
║  organization owner. It will not be shown again.                 ║
║                                                                  ║
║  Token also written to: %s
║  Delete it from logs and disk after first use.                   ║
╚══════════════════════════════════════════════════════════════════╝
`, token, pathLine)
	// Print directly to stderr so the banner survives even when slog is
	// configured to JSON output. Operators expect to see this in the
	// container log stream regardless of LOG_OUTPUT.
	fmt.Fprint(os.Stderr, banner)
}

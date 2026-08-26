#!/usr/bin/env bash
# gen-breachlist.sh
#
# Regenerates the embedded breached-password corpus consumed by the
# breachlist package (services/api/internal/breachlist/breached-passwords.bin).
# See docs/AUTHENTICATION.md § 4 for the provenance manifest to update
# afterward if N or the source changed.
#
# Two modes:
#
#   1. SEED (default) — hash a plaintext wordlist. This is what ships today:
#      a curated bootstrap seed of the most-prevalent breached passwords (all
#      public knowledge). N is small (a few hundred). It wires the full
#      breach-screen seam end-to-end while the real HIBP top-1M is a follow-up.
#
#        scripts/gen-breachlist.sh
#        scripts/gen-breachlist.sh seed                       # explicit
#        scripts/gen-breachlist.sh seed path/to/wordlist.txt  # custom wordlist
#
#   2. HIBP — decode HIBP's prevalence-ordered "HASH:count" file and take the
#      top-N. This is the documented swap to the real corpus. Download the
#      ordered file with HaveIBeenPwned/PwnedPasswordsDownloader (ordered mode)
#      or the published torrent, then:
#
#        scripts/gen-breachlist.sh hibp /path/to/pwned-passwords-ordered.txt 1000000
#
#      (third arg = N; default 1000000. The downloader tool is NOT embedded —
#       only the resulting digests are, so its licence does not propagate. See
#       docs/AUTHENTICATION.md § 4 for the licensing note.)
#
# After either mode the script prints the new N, byte size, and SHA-256, and
# tells you to update docs/AUTHENTICATION.md § 4 + NOTICE if N/source changed.
#
# Honesty note: the committed asset is named breached-passwords.bin (NOT
# pwned-top1m.bin) precisely because the shipped default is the bootstrap seed,
# not the full top-1M. Don't rename it just because you ran HIBP mode locally
# without committing — the name reflects what's in the repo.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
api_dir="$repo_root/services/api"
out="$api_dir/internal/breachlist/breached-passwords.bin"
seed_default="$api_dir/internal/breachlist/seed-wordlist.txt"

mode="${1:-seed}"

case "$mode" in
  seed)
    in="${2:-$seed_default}"
    echo "gen-breachlist: SEED mode — hashing wordlist $in" >&2
    ( cd "$api_dir" && go run ./cmd/breachlist-gen -in "$in" -out "$out" )
    ;;
  hibp)
    in="${2:?hibp mode requires the path to the HIBP ordered hash file}"
    n="${3:-1000000}"
    echo "gen-breachlist: HIBP mode — decoding top-$n of $in" >&2
    ( cd "$api_dir" && go run ./cmd/breachlist-gen -hibp -n "$n" -in "$in" -out "$out" )
    ;;
  *)
    echo "usage: $0 [seed [wordlist]] | [hibp <ordered-file> [N]]" >&2
    exit 2
    ;;
esac

# Report the resulting asset stats. These are the values to copy into
# docs/AUTHENTICATION.md § 4 on a real corpus swap.
bytes="$(wc -c < "$out" | tr -d ' ')"
records=$(( bytes / 20 ))
sha256="$(sha256sum "$out" | awk '{print $1}')"

echo "gen-breachlist: wrote $out" >&2
echo "  records (N): $records" >&2
echo "  bytes:       $bytes" >&2
echo "  sha256:      $sha256" >&2
echo >&2
echo "If N or the source changed, update docs/AUTHENTICATION.md § 4 and NOTICE." >&2

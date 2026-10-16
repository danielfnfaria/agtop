#!/usr/bin/env bash
# Compares `agtop --dump` of the TypeScript build with the Go one over the given Claude Code
# config directories (read-only: a temporary config file lists them). Prints the diff of the
# jq-normalized outputs (scannedAt dropped, keys sorted); exits 1 when they differ.
#
# Usage: AGTOP_TS_DIR=<checkout of the TypeScript agtop, built> scripts/parity-dump.sh <dir>...
set -euo pipefail

if [[ -z "${AGTOP_TS_DIR:-}" || ! -f "$AGTOP_TS_DIR/dist/cli.js" ]]; then
	echo "set AGTOP_TS_DIR to a built TypeScript agtop checkout (dist/cli.js)" >&2
	exit 2
fi
if [[ $# -eq 0 ]]; then
	echo "usage: $0 <claude config dir>..." >&2
	exit 2
fi
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }

root="$(cd "$(dirname "$0")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

config="$tmp/config.toml"
echo "[profiles]" >"$config"
for dir in "$@"; do
	label="$(basename "$dir")"
	label="${label#.}"
	printf '"%s" = "%s"\n' "$label" "$dir" >>"$config"
done

(cd "$root" && go build -o "$tmp/agtop" .)

normalize='del(.scannedAt)'
node "$AGTOP_TS_DIR/dist/cli.js" --dump --config "$config" | jq -S "$normalize" >"$tmp/ts.json"
"$tmp/agtop" --dump --config "$config" | jq -S "$normalize" >"$tmp/go.json"

if diff -u "$tmp/ts.json" "$tmp/go.json"; then
	echo "parity: identical ($(jq '.totals.sessions' "$tmp/go.json") sessions)"
else
	exit 1
fi

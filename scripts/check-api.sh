#!/bin/sh
set -eu

: "${APIDIFF_VERSION:?APIDIFF_VERSION is required}"

run_apidiff() {
	if [ -n "${GOLIB_APIDIFF:-}" ]; then
		go exec-tool "$GOLIB_APIDIFF" "$@"
	else
		go run "golang.org/x/exp/cmd/apidiff@${APIDIFF_VERSION}" "$@"
	fi
}

module=github.com/faustbrian/golib/pkg/log
baseline=api/log.txt
current=$(mktemp "${TMPDIR:-/tmp}/log-api.XXXXXX")
report=$(mktemp "${TMPDIR:-/tmp}/log-api-report.XXXXXX")
trap 'rm -f "$current" "$report"' EXIT HUP INT TERM

if [ ! -f "$baseline" ]; then
	printf 'missing API baseline: %s\n' "$baseline" >&2
	exit 1
fi

run_apidiff -m -w "$current" "$module"
run_apidiff -m -incompatible "$baseline" "$current" >"$report"

if [ -s "$report" ]; then
	printf '%s\n' 'incompatible exported API changes:' >&2
	cat "$report" >&2
	exit 1
fi

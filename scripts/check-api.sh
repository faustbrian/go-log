#!/bin/sh
set -eu

module=github.com/faustbrian/go-log
apidiff_version=v0.0.0-20250218142911-aa4b98e5adaa
baseline=api/go-log.txt
current=$(mktemp "${TMPDIR:-/tmp}/go-log-api.XXXXXX")
report=$(mktemp "${TMPDIR:-/tmp}/go-log-api-report.XXXXXX")
trap 'rm -f "$current" "$report"' EXIT HUP INT TERM

if [ ! -f "$baseline" ]; then
	printf 'missing API baseline: %s\n' "$baseline" >&2
	exit 1
fi

go run "golang.org/x/exp/cmd/apidiff@${apidiff_version}" \
	-m -w "$current" "$module"
go run "golang.org/x/exp/cmd/apidiff@${apidiff_version}" \
	-m -incompatible "$baseline" "$current" >"$report"

if [ -s "$report" ]; then
	printf '%s\n' 'incompatible exported API changes:' >&2
	cat "$report" >&2
	exit 1
fi

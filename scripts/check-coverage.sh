#!/bin/sh
set -eu

profile=$(mktemp "${TMPDIR:-/tmp}/log-coverage.XXXXXX")
report=$(mktemp "${TMPDIR:-/tmp}/log-coverage-report.XXXXXX")
trap 'rm -f "$profile" "$report"' EXIT HUP INT TERM

go test -covermode=atomic -coverprofile="$profile" ./...
go tool cover -func="$profile" | tee "$report"

awk '
$1 == "total:" {
	gsub(/%/, "", $3)
	found = 1
	if (($3 + 0) != 100) {
		printf "statement coverage is %s%%, want 100%%\n", $3 > "/dev/stderr"
		exit 1
	}
}
END {
	if (!found) {
		print "coverage report has no total" > "/dev/stderr"
		exit 1
	}
}
' "$report"

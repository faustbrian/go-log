#!/bin/sh
set -eu

unformatted=$(find . -type f -name '*.go' -not -path './.git/*' -exec gofmt -l {} +)
if [ -n "$unformatted" ]; then
	printf '%s\n' 'gofmt is required for:' >&2
	printf '%s\n' "$unformatted" >&2
	exit 1
fi

go mod tidy -diff

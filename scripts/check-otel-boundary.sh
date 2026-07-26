#!/bin/sh
set -eu

dependencies=$(go list -deps ./otel)
for forbidden in \
	go.opentelemetry.io/otel/sdk \
	github.com/faustbrian/golib/pkg/telemetry
do
	if printf '%s\n' "$dependencies" | grep -Eq "^${forbidden}(/|$)"; then
		printf 'otel bridge has forbidden dependency: %s\n' "$forbidden" >&2
		exit 1
	fi
done

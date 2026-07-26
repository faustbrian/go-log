#!/bin/sh
set -eu

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
	printf 'usage: %s vMAJOR.MINOR.PATCH [GIT_REF]\n' "$0" >&2
	exit 2
fi

version=$1
ref=${2:-$version}
if ! printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then
	printf 'invalid semantic version: %s\n' "$version" >&2
	exit 2
fi

git rev-parse --verify "${ref}^{commit}" >/dev/null
mkdir -p dist
archive="dist/log-${version}.tar.gz"

git archive --format=tar --prefix="log-${version}/" "$ref" |
	gzip -n -9 >"$archive"
shasum -a 256 "$archive" >"${archive}.sha256"

printf '%s\n' "$archive" "${archive}.sha256"

#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
	printf 'usage: %s vMAJOR.MINOR.PATCH\n' "$0" >&2
	exit 2
fi

version=${1#v}
awk -v heading="## [$version]" '
$0 == heading {
	found = 1
	next
}
found && /^## \[/ {
	exit
}
found {
	print
}
END {
	if (!found) {
		exit 1
	}
}
' CHANGELOG.md

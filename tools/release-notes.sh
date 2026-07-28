#!/bin/sh
# Print one version's section of CHANGELOG.md, for a release body.
#
#   sh tools/release-notes.sh 0.1.0 > notes.md
#
# The release page should say what changed, and the changelog already does.
# Copying it by hand is how the two drift apart, and the one that drifts is
# always the release page, because nobody re-reads it.
#
# Exits non-zero when the version has no section. That is deliberate: a tag
# pushed without a changelog entry should fail the release rather than publish
# an empty body.
set -eu

ver="${1:-}"
[ -n "$ver" ] || { echo "usage: $0 <version>" >&2; exit 2; }
ver="${ver#v}"

changelog="${2:-CHANGELOG.md}"
[ -f "$changelog" ] || { echo "$changelog not found" >&2; exit 2; }

# From the heading for this version up to the next version heading. The link
# definitions at the bottom of the file are not part of any section, so they
# are dropped along with anything else after the last blank line run.
body="$(awk -v want="$ver" '
	/^## / {
		# "## [0.1.0] - 2026-07-28" -> "0.1.0"
		line = $0
		sub(/^## +\[?/, "", line)
		sub(/\].*$/, "", line)
		sub(/ +-.*$/, "", line)
		found = (line == want)
		if (found) { next }
		if (inside) { exit }
	}
	found && !inside { inside = 1 }
	inside && !/^\[[^]]+\]: / { print }
' "$changelog")"

# Trim leading and trailing blank lines without needing GNU tools.
body="$(printf '%s\n' "$body" | sed -e '/./,$!d' | awk '
	{ lines[NR] = $0 }
	END {
		last = NR
		while (last > 0 && lines[last] ~ /^[[:space:]]*$/) last--
		for (i = 1; i <= last; i++) print lines[i]
	}
')"

if [ -z "$body" ]; then
	echo "no CHANGELOG section for version $ver" >&2
	exit 1
fi

printf '%s\n' "$body"

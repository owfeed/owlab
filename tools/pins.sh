#!/bin/sh
# The release pins that are not images/owlab.yaml, and the two things worth
# doing to them.
#
#   sh tools/pins.sh check              # do the copies still agree with the config?
#   sh tools/pins.sh bump 25.12.4 25.12.5   # move every copy of one release
#
# owlab pins hard -- a rootfs and its feed have to name the same point release
# or every install fails -- so the number is copied into every place that
# starts a router. `images/owlab.yaml` is the one a person edits. Everything
# else is a copy, and a copy goes stale without failing: an old release still
# builds, it just stops being the release anyone runs. Measured on 2026-09-04,
# `ARG BASE_IMAGE` and both release literals in `ci.yml` still said 25.12.4
# while the config had been on 25.12.5 since it was written, and every job was
# green the whole time.
#
# `check` is the gate in ci.yml. `bump` is what pins.yml runs before opening a
# pull request. They share this file so that "where the pins live" is one list
# with one meaning, rather than a workflow and a checker that can disagree
# about which files count.
set -eu

CONFIG=images/owlab.yaml

# The files this repository RUNS a release number out of. Not the ones that
# print one.
#
# Deliberately outside the list, and this is the whole reason it is narrow --
# a check that fires on a healthy tree is worse than no check:
#
#   README*.md        the sample `owlab releases` output shows a pin one
#                     release behind ON PURPOSE; it is what the command looks
#                     like when it has something to say
#   docs/, CHANGELOG  prose about releases that happened, and history that
#                     cannot be rewritten
#   examples/         configs a reader copies into their own repository, where
#                     the release is an illustration and not this repo's pin
#   action/action.yml input documentation, inside a YAML block scalar
#   internal/, cmd/   test fixtures and the examples in error messages
#
# $CONFIG is not here either: it is the answer, not a copy of it.
pin_files() {
	for f in .github/workflows/*.yml images/Dockerfile; do
		[ -f "$f" ] || continue
		echo "$f"
	done
}

# Every release literal on a line that is not a comment, as `file:line:release`.
#
# Comments are skipped because in these files they are evidence. `images/Dockerfile`
# explains a real apk failure with "a 25.12.4 rootfs pointed at the 25.12.5 feed",
# and `images.yml` explains the resolver with "upstream tagging 25.12.5". Holding
# those to the current pin would either fire on a healthy tree or, worse, let
# `bump` rewrite a measurement into something that was never measured.
#
# Two digits, a dot, two digits, a dot: an OpenWrt release is YY.MM.N. The
# tokeniser matches any three-part number and then filters, so `v7.0.1`,
# `ubuntu-24.04-arm` and `go 1.24.3` are read and discarded rather than never
# looked at -- a regex tuned tightly enough to skip them silently would also
# skip a release nobody meant to leave behind.
literals() {
	awk '
		/^[[:space:]]*#/ { next }
		{
			s = $0
			while (match(s, /[0-9]+\.[0-9]+\.[0-9]+/)) {
				v = substr(s, RSTART, RLENGTH)
				if (v ~ /^[0-9][0-9]\.[0-9][0-9]\.[0-9]+$/) {
					print FILENAME ":" FNR ":" v
				}
				s = substr(s, RSTART + RLENGTH)
			}
		}
	' "$1"
}

cmd_check() {
	pins="$(sed -n 's/^[[:space:]]*release:[[:space:]]*"\([0-9][0-9.]*\)".*/\1/p' "$CONFIG" | sort -u)"

	# An extractor that found nothing is not a clean tree. Without this the
	# check passes hardest exactly when it has stopped working -- somebody
	# reformats the config, no pin is ever read again, and every literal below
	# is compared against an empty list and reported as wrong. Which is the
	# louder half of the failure; the quiet half is that `bump` then has
	# nothing to move.
	if [ -z "$pins" ]; then
		echo "$CONFIG: no 'release:' pins found" >&2
		echo "  the extractor in tools/pins.sh is broken, not the tree" >&2
		exit 1
	fi
	echo "$CONFIG pins: $(echo "$pins" | tr '\n' ' ')"

	bad=0
	for f in $(pin_files); do
		for hit in $(literals "$f"); do
			v="${hit##*:}"
			if printf '%s\n' "$pins" | grep -qxF "$v"; then
				continue
			fi
			echo "  ${hit%:*}: $v is not a release $CONFIG pins"
			bad=$((bad + 1))
		done
	done

	if [ "$bad" -gt 0 ]; then
		echo
		echo "$bad release literal(s) name a release $CONFIG does not pin." >&2
		echo "This is the copy that was forgotten when the pin moved: CI keeps" >&2
		echo "testing a release nobody runs, and nothing goes red to say so." >&2
		echo >&2
		echo "Fix, in whichever direction is true:" >&2
		echo "  sh tools/pins.sh bump <the release above> <the pinned release>" >&2
		echo "  or move the pin in $CONFIG and run it the other way round" >&2
		echo >&2
		echo "Re-check with: sh tools/pins.sh check" >&2
		exit 1
	fi
	echo "every release literal in the pin files is one $CONFIG pins"
}

cmd_bump() {
	old="${1:-}"
	new="${2:-}"
	if [ -z "$old" ] || [ -z "$new" ]; then
		echo "usage: sh tools/pins.sh bump <old release> <new release>" >&2
		exit 2
	fi

	# Token replacement rather than sed, for one reason: `24.10.1` is a prefix
	# of `24.10.10`, and a branch reaching double digits is a matter of time.
	# The same tokeniser as `literals` above, so what gets rewritten is exactly
	# what gets checked.
	for f in $CONFIG $(pin_files); do
		awk -v old="$old" -v new="$new" '
			/^[[:space:]]*#/ { print; next }
			{
				s = $0
				out = ""
				while (match(s, /[0-9]+\.[0-9]+\.[0-9]+/)) {
					v = substr(s, RSTART, RLENGTH)
					out = out substr(s, 1, RSTART - 1)
					out = out (v == old ? new : v)
					s = substr(s, RSTART + RLENGTH)
				}
				print out s
			}
		' "$f" > "$f.pins.tmp"
		if cmp -s "$f" "$f.pins.tmp"; then
			rm -f "$f.pins.tmp"
			continue
		fi
		mv "$f.pins.tmp" "$f"
		echo "$f: $old -> $new"
	done
}

case "${1:-}" in
check)
	cmd_check
	;;
bump)
	shift
	cmd_bump "$@"
	;;
*)
	echo "usage: sh tools/pins.sh check | bump <old release> <new release>" >&2
	exit 2
	;;
esac

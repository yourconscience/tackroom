#!/bin/sh
# <xbar.title>tackroom</xbar.title>
# <xbar.desc>The sync state and the failing scheduled jobs of tackroom, from tackroom dash --json.</xbar.desc>
# <xbar.dependencies>tackroom</xbar.dependencies>
# <xbar.abouturl>https://github.com/yourconscience/tackroom/blob/main/swiftbar/RECIPE.md</xbar.abouturl>

# tackroom in the macOS menu bar, through SwiftBar. Needs tackroom 3.3 or later.
# Install and menu: RECIPE.md beside this file. Uses only what macOS ships:
# /bin/sh, plutil, sed, tr, head, mktemp and date.
#
# The mark is an SF Symbol (sfimage). Set TACKROOM_SWIFTBAR_PLAIN to anything
# for a plain-text mark instead. TACKROOM_BIN is the path to tackroom, when it
# is in none of the places searched below.

# clean TEXT prints TEXT as the text of one menu line: no control characters,
# no "|" (it would start the parameters), no double quote (it quotes a
# parameter's value), and no leading "-" (a line that starts with "--" is not
# an item).
clean() {
	printf '%s' "$1" | LC_ALL=C tr '\000-\037\177' ' ' |
		LC_ALL=C sed -e 's/|/¦/g' -e 's/"/″/g' -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e 's/^-/‐/'
}

# row TEXT PARAMS prints a menu line whose text came from outside the script.
row() {
	text=$(clean "$1")
	[ -n "$text" ] || return 0
	printf '%s | symbolize=false emojize=false length=100 %s\n' "$text" "$2"
}

# safe VALUE is true for a value that can be one parameter of an action and
# one argument of tackroom: letters, digits, ".", "_", "@" and "-", and not
# starting with "-". Anything else gets no action.
safe() {
	case $1 in
	'' | [!A-Za-z0-9]* | *[!A-Za-z0-9._@-]*) return 1 ;;
	esac
}

# safe_path is the same for the absolute path of tackroom, which may also have "/" and "+".
safe_path() {
	case $1 in
	/*) ;;
	*) return 1 ;;
	esac
	case $1 in
	*[!A-Za-z0-9._/@+-]*) return 1 ;;
	esac
}

# num prints a count, or 0 for anything else.
num() {
	case $1 in
	'' | *[!0-9]*) echo 0 ;;
	*) echo "$1" ;;
	esac
}

# mark LEVEL COUNT prints the line for the menu bar: ok, fail or anything else
# (a warning), and how many things fail or need attention when there are any.
mark() {
	case $1 in
	ok)
		sym=checkmark.circle
		text='✓'
		;;
	fail)
		sym=exclamationmark.octagon
		text='✖'
		;;
	*)
		sym=exclamationmark.triangle
		text='▲'
		;;
	esac
	n=
	[ "$2" -gt 0 ] && n=$2
	if [ -n "${TACKROOM_SWIFTBAR_PLAIN:-}" ]; then
		printf '%s\n' "$text${n:+ $n}"
	else
		printf '%s | sfimage=%s\n' "$n" "$sym"
	fi
}

# problem TEXT prints the whole menu for when nothing can be read, and ends.
problem() {
	mark warn 0
	printf '%s\n' ---
	row "$1" ''
	printf '%s\n' ---
	printf '%s\n' 'Refresh | refresh=true'
	exit 0
}

# SwiftBar starts plugins with a minimal environment, so look in the usual
# places before PATH.
bin=
for candidate in "${TACKROOM_BIN:-}" "$HOME/.local/bin/tackroom" /opt/homebrew/bin/tackroom /usr/local/bin/tackroom; do
	if [ -n "$candidate" ] && [ -f "$candidate" ] && [ -x "$candidate" ]; then
		bin=$candidate
		break
	fi
done
[ -n "$bin" ] || bin=$(command -v tackroom 2>/dev/null)
[ -n "$bin" ] || problem 'tackroom not found. Install it, or set TACKROOM_BIN to its path.'

err=$(mktemp -t tackroom-swiftbar) || problem 'Could not create a temporary file.'
trap 'rm -f "$err"' EXIT
json=$("$bin" dash --json 2>"$err")
rc=$?
if [ "$rc" -ne 0 ]; then
	reason=$(head -n 1 "$err")
	# An older tackroom does not know the flag.
	case "$reason" in
	*'not defined: -json'*) problem 'This tackroom has no dash --json. It needs tackroom 3.3 or later.' ;;
	esac
	problem "tackroom dash --json failed (exit $rc)${reason:+: $reason}"
fi

# get KEY prints one value of the document; a nested key is a path, such as
# jobs.0.name, and an array gives its length.
get() {
	printf '%s\n' "$json" | plutil -extract "$1" raw -o - - 2>/dev/null
}

level=$(get level) || problem 'tackroom dash --json did not print JSON. It needs tackroom 3.3 or later.'
count=$(($(num "$(get failing)") + $(num "$(get attention)")))

mark "$level" "$count"
printf '%s\n' ---
row "$(get title)" ''
notes=$(num "$(get notes)")
i=0
while [ "$i" -lt "$notes" ]; do
	row "$(get "notes.$i")" ''
	i=$((i + 1))
done

# Actions run tackroom by its path, with every value its own parameter, and
# only when the path and the values are plain enough to be one parameter each.
actions=
safe_path "$bin" && actions=1

njobs=$(num "$(get jobs)")
[ "$njobs" -gt 0 ] && printf '%s\n' ---
i=0
while [ "$i" -lt "$njobs" ]; do
	host=$(get "jobs.$i.host")
	src=$(get "jobs.$i.source")
	id=$(get "jobs.$i.id")
	name=$(get "jobs.$i.name")
	health=$(get "jobs.$i.health")
	result=$(get "jobs.$i.result")
	label=${name:-$id}
	remote=
	if [ -n "$host" ] && [ "$host" != local ]; then
		remote=1
		label="$label on $host"
	fi
	label="$label${result:+: $result}"
	case $health in
	fail) sym=xmark.circle ;;
	*) sym=exclamationmark.circle ;;
	esac
	row "$label" "sfimage=$sym"
	if [ "$src" = launchd ] && [ -n "$actions" ] && safe "$id" && safe "${host:-local}"; then
		if [ -n "$remote" ]; then
			printf 'Run now | sfimage=play.fill bash=%s param1=cron param2=run param3=--host param4=%s param5=%s terminal=false refresh=true\n' "$bin" "$host" "$id"
		else
			printf 'Run now | sfimage=play.fill bash=%s param1=cron param2=run param3=%s terminal=false refresh=true\n' "$bin" "$id"
		fi
	fi
	i=$((i + 1))
done

printf '%s\n' ---
if [ -n "$actions" ]; then
	printf 'Sync now | bash=%s param1=sync terminal=false refresh=true\n' "$bin"
	printf 'Open view | bash=%s param1=view terminal=false\n' "$bin"
else
	row 'Actions are off: the path to tackroom has a space or an unusual character.' 'color=#888888'
fi
printf '%s\n' 'Refresh | refresh=true'

# checked_at is UTC; show it in local time.
checked=$(get checked_at)
if epoch=$(TZ=UTC date -j -f '%Y-%m-%dT%H:%M:%SZ' "$checked" +%s 2>/dev/null); then
	checked=$(date -r "$epoch" +%H:%M)
else
	checked=$(date +%H:%M)
fi
printf '%s\n' ---
printf 'Checked %s | color=#888888\n' "$checked"

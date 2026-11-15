# tackroom hosts probe. Runs under `sh -s -- TOKEN BIN CONFIG`, locally or
# over SSH, and prints this machine's tackroom state as sections that start
# with a "TOKEN kind<TAB>key" line, as crons_probe.sh does. It runs only
# `tackroom version`, `tackroom status --json` and read-only git commands;
# with no tackroom it only looks for brew and npm.
# BIN is the tackroom to ask; empty means look for one. CONFIG, when set, is
# passed to status as --config.
T="$1"
bin="$2"
cfg="$3"
sec() { printf '\n%s %s\t%s\n' "$T" "$1" "$2"; }
run() { kind=$1; key=$2; shift 2; sec "$kind" "$key"; "$@" 2>&1; rc=$?; sec rc "$rc"; }

# A non-login SSH shell has a short PATH, so after PATH look where installers
# put programs. Sets found to the program and how to path, dir or none.
look() {
	how=path
	found=$(command -v "$1" 2>/dev/null)
	[ -n "$found" ] && return
	how=none
	for d in "$HOME/.local/bin" "$HOME/go/bin" /opt/homebrew/bin /usr/local/bin; do
		if [ -x "$d/$1" ]; then
			found="$d/$1"
			how=dir
			return
		fi
	done
}

# The key says how tackroom was found: given, path, dir or none.
how=given
if [ -z "$bin" ]; then
	look tackroom
	bin=$found
fi
run bin "$how" printf %s "$bin"
if [ -z "$bin" ]; then
	# What `tackroom hosts setup --install` can install tackroom with.
	look brew
	run brew "" printf %s "$found"
	look npm
	run npm "" printf %s "$found"
	exit 0
fi

run version "" "$bin" version

# status exits 1 when anything is out of sync; its JSON is still the answer.
# --skip-package-age keeps it off the registries; a tackroom without the flag
# is asked again without it.
st() {
	if [ -n "$cfg" ]; then "$bin" status --json "$@" --config "$cfg"; else "$bin" status --json "$@"; fi
}
out=$(st --skip-package-age 2>&1)
rc=$?
case $out in *"flag provided but not defined: -skip-package-age"*)
	out=$(st 2>&1)
	rc=$?
	;;
esac
sec status ""
printf '%s\n' "$out"
sec rc "$rc"

# The config root: what status reports, else where tackroom looks by default.
root=$(printf '%s\n' "$out" | sed -n 's/^  "config_root": "\(.*\)",*$/\1/p' | head -n 1)
if [ -z "$root" ]; then
	if [ -n "$cfg" ]; then
		root=$(dirname "$cfg")
	else
		root="${TACKROOM_HOME:-$HOME/.agents}"
		case $root in "~/"*) root="$HOME/${root#\~/}" ;; esac
	fi
fi
run root "" printf %s "$root"

if command -v git >/dev/null 2>&1 && [ -d "$root" ]; then
	run commit "" git -C "$root" rev-parse HEAD
	run committed "" git -C "$root" log -1 --format=%ct
	run dirty "" sh -c 'git -C "$1" status --porcelain | wc -l' sh "$root"
	# rc 0 when tackroom.yaml is committed, so other machines can pull it.
	run tracked "" git -C "$root" ls-files --error-unmatch tackroom.yaml
	run lock "" test -f "$root/tackroom.lock"
fi
exit 0

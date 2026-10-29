# tackroom crons probe. Runs under `sh -s -- TOKEN`, locally or over SSH, and
# prints raw scheduler state as sections that start with a "TOKEN kind<TAB>key"
# line. It only lists, reads and prints; it never writes on the host. tackroom
# parses the sections locally.
T="$1"
sec() { printf '\n%s %s\t%s\n' "$T" "$1" "$2"; }
run() { kind=$1; key=$2; shift 2; sec "$kind" "$key"; "$@" 2>&1; rc=$?; sec rc "$rc"; }
mtime() { stat -c %Y "$1" 2>/dev/null || stat -f %m "$1"; }
b64() { base64 < "$1"; }
# Absolute or ~/ paths in stdin, one per line: bare tokens, plus whole quoted
# or plist <string> values so paths with spaces are checked as a whole.
paths() {
	t=$(cat)
	{
		printf '%s\n' "$t" | grep -oE '(^|[[:space:]>="'"'"'(:])~?/[A-Za-z0-9._/+@%-]+' | sed -E 's/^[^~/]//'
		printf '%s\n' "$t" | grep -oE "<string>~?/[^<]*</string>|\"~?/[^\"]*\"|'~?/[^']*'" | sed -E 's/^(<string>|["'"'"'])//; s/(<\/string>|["'"'"'])$//'
	} | sort -u
}
stat_paths() {
	while IFS= read -r p; do
		case $p in "~/"*) f="$HOME/${p#\~/}" ;; *) f=$p ;; esac
		run mtime "$p" mtime "$f"
	done
}

os=$(uname -s)
run os "" printf %s "$os"
run tz "" readlink /etc/localtime
run now "" date +%s
uid=$(id -u)

if [ "$os" = Darwin ]; then
	run launchd-disabled "" launchctl print-disabled "gui/$uid"
	for f in "$HOME"/Library/LaunchAgents/*.plist; do
		[ -e "$f" ] || continue
		run plist "$f" plutil -convert json -o - "$f"
		label=$(plutil -extract Label raw -o - "$f" 2>/dev/null)
		[ -n "$label" ] && run launchctl "$f" launchctl print "gui/$uid/$label"
		plutil -convert xml1 -o - "$f" 2>/dev/null | paths | stat_paths
	done
fi

if command -v crontab >/dev/null 2>&1; then
	run crontab "" crontab -l
	crontab -l 2>/dev/null | grep -v '^[[:space:]]*#' | paths | stat_paths
fi

if [ "$os" = Linux ] && command -v systemctl >/dev/null 2>&1; then
	run timers "" systemctl --user list-timers --all --output=json
	for t in $(systemctl --user list-units --type=timer --all --no-legend --plain 2>/dev/null | awk '{print $1}'); do
		run timer "$t" systemctl --user show "$t" -p Id -p TimersCalendar -p TimersMonotonic -p Triggers -p ActiveState -p FragmentPath
		svc=$(systemctl --user show "$t" -p Triggers --value 2>/dev/null)
		[ -n "$svc" ] && run service "$t" systemctl --user show "$svc" -p ExecStart -p ExecMainStatus -p ExecMainStartTimestamp -p Result -p StandardOutput
	done
fi

h="$HOME/.hermes/cron"
if [ -e "$h/jobs.json" ]; then
	run hermes-jobs "" cat "$h/jobs.json"
	for f in executions.db executions.db-wal; do
		[ -e "$h/$f" ] && run hermes-db "$f" b64 "$h/$f"
	done
	[ -e "$h/ticker_heartbeat" ] && run hermes-heartbeat "" cat "$h/ticker_heartbeat"
fi

c="$HOME/.codex/sqlite"
if [ -e "$c/codex-dev.db" ]; then
	for f in codex-dev.db codex-dev.db-wal; do
		[ -e "$c/$f" ] && run codex-db "$f" b64 "$c/$f"
	done
fi

if [ "$os" = Darwin ]; then
	s="$HOME/Library/Application Support/Claude/claude-code-sessions"
	for f in "$s"/*/*/scheduled-tasks.json; do
		[ -e "$f" ] && run claude-tasks "$f" cat "$f"
	done
	if [ -d "$s" ]; then
		grep -rl --include='*.json' '"scheduledTaskId"' "$s" 2>/dev/null | while IFS= read -r f; do
			run claude-run "$f" cat "$f"
		done
	fi
	for f in "$HOME"/.claude/scheduled-tasks/*/SKILL.md; do
		[ -e "$f" ] && run claude-skill "$f" head -c 2000 "$f"
	done
fi
exit 0

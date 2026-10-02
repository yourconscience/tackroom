#!/usr/bin/env sh
# Cut a tackroom release: verify, tag, push. CI does the rest.
#
# Usage: scripts/release.sh vX.Y.Z [--yes|-y]
#
# Pushing a v* tag triggers .github/workflows/release.yml, which re-verifies the
# tag against main and a green ci.yml run, waits on the protected `release`
# environment, then publishes binaries, the Homebrew tap, and the npm wrapper.
# Tagging only happens after the release PR is reviewed and merged, and after
# explicit approval -- this script is the approval step, so it must be run on
# purpose, from a clean up-to-date main.
#
# Set TACKROOM_RELEASE_LIB=1 to source the helpers (used by release_test.sh)
# without running a release.

set -eu

SEMVER_RE='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
NPM_PACKAGE='tackroom'
HOMEBREW_FORMULA='tackroom'

usage() {
	cat <<'EOF'
usage: scripts/release.sh vX.Y.Z [--yes|-y]

Runs the release checks, then creates and pushes the annotated tag. CI publishes
the GitHub release, the Homebrew tap bump, and the npm wrapper from that tag.
EOF
}

die() {
	echo "error: $*" >&2
	exit 1
}

note() {
	echo ">> $*"
}

# valid_tag reports whether the argument is a strict stable semver tag.
valid_tag() {
	printf '%s' "${1:-}" | grep -Eq "$SEMVER_RE"
}

# version_gt reports whether $1 is a greater stable version than $2.
version_gt() {
	newer=${1#v}
	older=${2#v}
	newer_major=${newer%%.*}
	newer_rest=${newer#*.}
	newer_minor=${newer_rest%%.*}
	newer_patch=${newer_rest#*.}
	older_major=${older%%.*}
	older_rest=${older#*.}
	older_minor=${older_rest%%.*}
	older_patch=${older_rest#*.}
	[ "$newer_major" -gt "$older_major" ] && return 0
	[ "$newer_major" -lt "$older_major" ] && return 1
	[ "$newer_minor" -gt "$older_minor" ] && return 0
	[ "$newer_minor" -lt "$older_minor" ] && return 1
	[ "$newer_patch" -gt "$older_patch" ] && return 0
	return 1
}

# check_clean_tree refuses to tag anything but an exactly committed tree.
# Untracked files are refused too: they usually mean uncommitted work, and a
# release should be reproducible from the tagged commit alone.
check_clean_tree() {
	git diff --quiet || die "working tree has unstaged changes"
	git diff --cached --quiet || die "index has staged changes"
	[ -z "$(git ls-files -u)" ] || die "working tree has unmerged files"
	untracked=$(git ls-files --others --exclude-standard)
	[ -z "$untracked" ] || die "untracked files present: $(printf '%s' "$untracked" | tr '\n' ' ')"
}

run_checks() {
	note "go test ./..."
	go test ./...

	note "go build ./..."
	go build ./...

	note "go vet ./..."
	go vet ./...

	if command -v python3 >/dev/null 2>&1; then
		note "python3 memory tests"
		python3 -m unittest discover -s memory/tests
	else
		note "python3 not found: skipped memory tests"
	fi

	if command -v node >/dev/null 2>&1; then
		note "node --test npm/install.test.js"
		node --test npm/install.test.js
	else
		note "node not found: skipped npm wrapper tests"
	fi

	note "scripts/release_test.sh"
	sh "$(dirname -- "$0")/release_test.sh"

	if command -v goreleaser >/dev/null 2>&1; then
		note "goreleaser check"
		goreleaser check
	else
		note "goreleaser not found: skipped config check (CI runs goreleaser)"
	fi
}

confirm() {
	if [ "$YES" = "--yes" ] || [ "$YES" = "-y" ]; then
		return 0
	fi
	printf '%s [y/N] ' "$1"
	read -r answer
	[ "$answer" = y ] || [ "$answer" = Y ]
}

main() {
	TAG="${1:-}"
	YES="${2:-}"

	[ -n "$TAG" ] || { usage >&2; exit 1; }
	valid_tag "$TAG" || { usage >&2; die "tag must look like v0.9.0"; }
	[ -f .goreleaser.yaml ] || die "run from the tackroom repo root"

	branch=$(git rev-parse --abbrev-ref HEAD)
	[ "$branch" = "main" ] || die "must be on main (on $branch)"
	check_clean_tree

	git fetch origin --tags -q
	[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || die "HEAD is not in sync with origin/main"

	previous=$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || true)
	if [ -n "$previous" ]; then
		version_gt "$TAG" "$previous" || die "$TAG is not newer than $previous"
	fi
	git rev-parse -q --verify "refs/tags/$TAG" >/dev/null && die "$TAG already exists"

	run_checks

	if [ -n "$previous" ]; then
		note "changes since $previous:"
		git log --oneline "$previous"..HEAD | sed 's/^/   /'
	fi

	confirm "release $TAG?" || die "aborted"

	git tag -a "$TAG" -m "$TAG" || die "failed to create $TAG"
	git push origin "$TAG" || die "failed to push $TAG (the local tag is still present; delete it with 'git tag -d $TAG' before retrying)"

	echo ">> tag $TAG pushed. CI publishes binaries, brew tap, and npm."
	echo ">> watch: gh run watch --interval 30 \$(gh run list --workflow=release.yml --limit 1 --json databaseId -q '.[0].databaseId')"
	echo ">> after CI: brew info $HOMEBREW_FORMULA && npm view $NPM_PACKAGE version"
}

[ "${TACKROOM_RELEASE_LIB:-}" = "1" ] || main "$@"

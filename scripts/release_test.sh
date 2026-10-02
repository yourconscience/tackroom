#!/usr/bin/env sh
# Tests for the validation helpers in scripts/release.sh.
#
# Sourcing the script with TACKROOM_RELEASE_LIB=1 exposes valid_tag,
# version_gt, and check_clean_tree without running a release.

set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
TACKROOM_RELEASE_LIB=1 . "$here/release.sh"

failures=0

fail() {
	echo "FAIL: $*" >&2
	failures=$((failures + 1))
}

accept_tag() {
	valid_tag "$1" || fail "expected tag '$1' to be accepted"
}

reject_tag() {
	if valid_tag "$1"; then
		fail "expected tag '$1' to be rejected"
	fi
}

accept_newer() {
	version_gt "$1" "$2" || fail "expected $1 to be newer than $2"
}

reject_newer() {
	if version_gt "$1" "$2"; then
		fail "expected $1 to not be newer than $2"
	fi
}

# Strict stable semver only.
accept_tag v0.1.0
accept_tag v0.9.0
accept_tag v1.0.0
accept_tag v10.20.30
reject_tag ""
reject_tag 1.2.3
reject_tag v1.2
reject_tag v1.2.3.4
reject_tag v1.2.3-rc.1
reject_tag v1.2.3+build
reject_tag v01.2.3
reject_tag v1.02.3
reject_tag v1x.2y.3z
reject_tag V1.2.3
reject_tag v1.2.x

# Version ordering.
accept_newer v0.9.0 v0.8.0
accept_newer v0.10.0 v0.9.9
accept_newer v1.0.0 v0.99.99
accept_newer v0.2.1 v0.2.0
reject_newer v0.8.0 v0.9.0
reject_newer v0.9.0 v0.9.0
reject_newer v1.0.0 v1.0.0

# Clean-tree detection in a throwaway repository.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

(
	cd "$tmp"
	git init -q .
	git config user.email release-test@example.com
	git config user.name "release test"
	printf 'tracked\n' > tracked.txt
	git add tracked.txt
	git commit -qm "initial"

	check_clean_tree || fail "expected a freshly committed tree to be clean"

	printf 'changed\n' >> tracked.txt
	if (check_clean_tree) 2>/dev/null; then
		fail "expected unstaged changes to be refused"
	fi
	git checkout -q -- tracked.txt

	printf 'staged\n' > staged.txt
	git add staged.txt
	if (check_clean_tree) 2>/dev/null; then
		fail "expected staged changes to be refused"
	fi
	git rm -q --cached staged.txt
	rm -f staged.txt

	printf 'untracked\n' > untracked.txt
	if (check_clean_tree) 2>/dev/null; then
		fail "expected untracked files to be refused"
	fi
	rm -f untracked.txt

	check_clean_tree || fail "expected the tree to be clean again"
) || failures=$((failures + 1))

if [ "$failures" -ne 0 ]; then
	echo "release_test.sh: $failures failure(s)" >&2
	exit 1
fi

echo "release_test.sh: ok"

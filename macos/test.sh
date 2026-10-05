#!/bin/sh
# Runs the Swift tests. With only the Command Line Tools installed, SwiftPM
# does not find the Testing framework on its own, so point it there.
set -eu
cd "$(dirname "$0")"
clt=/Library/Developer/CommandLineTools/Library/Developer
if [ -d "$clt/Frameworks/Testing.framework" ] && ! xcode-select -p | grep -q Xcode.app; then
	exec swift test -Xswiftc -F -Xswiftc "$clt/Frameworks" -Xlinker -F -Xlinker "$clt/Frameworks" \
		-Xlinker -rpath -Xlinker "$clt/Frameworks" -Xlinker -rpath -Xlinker "$clt/usr/lib"
fi
exec swift test

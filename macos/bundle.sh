#!/bin/sh
# Builds Tackroom.app from this package with only the Command Line Tools.
# Usage: ./bundle.sh [--install]    --install copies the app to ~/Applications.
set -eu
cd "$(dirname "$0")"

swift build -c release
bin="$(swift build -c release --show-bin-path)/Tackroom"
app=build/Tackroom.app
iconset=build/AppIcon.iconset

rm -rf "$app" "$iconset"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$bin" "$app/Contents/MacOS/Tackroom"
cp Info.plist "$app/Contents/Info.plist"
"$bin" --write-iconset "$iconset"
iconutil -c icns "$iconset" -o "$app/Contents/Resources/AppIcon.icns"
codesign --force --sign - "$app"
echo "built $app"

if [ "${1:-}" = "--install" ]; then
	mkdir -p "$HOME/Applications"
	rm -rf "$HOME/Applications/Tackroom.app"
	cp -R "$app" "$HOME/Applications/"
	echo "installed $HOME/Applications/Tackroom.app"
fi

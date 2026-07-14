#!/bin/sh
# Build cmd/tether-gui into a macOS .app bundle. macOS only (uses the Go
# darwin/cgo toolchain). Usage: package-macos.sh <version> <out-dir>
set -eu

VERSION="${1:-dev}"
OUT="${2:-dist}"
APP="$OUT/Tether.app"

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS"

# The status icon is generated at runtime, so no Resources icon is required.
go build -o "$APP/Contents/MacOS/Tether" ./cmd/tether-gui

# Bundle the CLI inside the app so the cask can symlink it onto PATH — the app
# is then fully self-contained (no separate formula dependency).
go build -ldflags "-X main.Version=${VERSION}" -o "$APP/Contents/MacOS/tether" ./cmd/tether

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleName</key>
    <string>Tether</string>
    <key>CFBundleDisplayName</key>
    <string>Tether</string>
    <key>CFBundleExecutable</key>
    <string>Tether</string>
    <key>CFBundleIdentifier</key>
    <string>io.github.mwdomino.tether</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleVersion</key>
    <string>${VERSION}</string>
    <key>CFBundleShortVersionString</key>
    <string>${VERSION}</string>
    <key>LSMinimumSystemVersion</key>
    <string>11.0</string>
    <key>LSUIElement</key>
    <true/>
    <key>NSHighResolutionCapable</key>
    <true/>
</dict>
</plist>
PLIST

echo "built $APP (version ${VERSION})"

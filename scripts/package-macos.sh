#!/bin/sh
# Build cmd/tether-gui into a macOS .app bundle. macOS only (uses the Go
# darwin/cgo toolchain). Usage: package-macos.sh <version> <out-dir>
set -eu

VERSION="${1:-dev}"
OUT="${2:-dist}"
APP="$OUT/Tether.app"

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

go build -o "$APP/Contents/MacOS/Tether" ./cmd/tether-gui

# App icon: build Tether.icns from the 1024px rope-knot PNG. Requires macOS
# tools; skipped (with a warning) elsewhere so the script still runs on Linux.
SRC="cmd/tether-gui/assets/appicon.png"
if command -v sips >/dev/null 2>&1 && command -v iconutil >/dev/null 2>&1; then
  ICONSET="$OUT/Tether.iconset"
  rm -rf "$ICONSET"; mkdir -p "$ICONSET"
  for s in 16 32 128 256 512; do
    sips -z "$s" "$s" "$SRC" --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
    s2=$((s * 2))
    sips -z "$s2" "$s2" "$SRC" --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
  done
  iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/Tether.icns"
  rm -rf "$ICONSET"
else
  echo "warning: sips/iconutil not found; app icon not bundled" >&2
fi

# Bundle the CLI inside the app so the cask can symlink it onto PATH — the app
# is then fully self-contained (no separate formula dependency). Note: the
# name must differ from "Tether" by more than case, since the default macOS
# filesystem is case-insensitive (otherwise the CLI clobbers the GUI binary).
go build -ldflags "-X main.Version=${VERSION}" -o "$APP/Contents/MacOS/tether-cli" ./cmd/tether

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
    <key>CFBundleIconFile</key>
    <string>Tether</string>
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

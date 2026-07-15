#!/usr/bin/env bash
set -euo pipefail

VERSION="0.3.1"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="$ROOT/build"
DIST_DIR="$ROOT/dist"
APP="$DIST_DIR/CutBridge.app"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "此脚本必须在 macOS 上运行。" >&2
  exit 1
fi

for command in go lipo ditto; do
  command -v "$command" >/dev/null 2>&1 || {
    echo "缺少命令：$command" >&2
    exit 1
  }
done

rm -rf "$BUILD_DIR" "$DIST_DIR"
mkdir -p "$BUILD_DIR" "$APP/Contents/MacOS" "$APP/Contents/Resources"

pushd "$ROOT" >/dev/null
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build \
  -trimpath -ldflags="-s -w" \
  -o "$BUILD_DIR/CutBridge-amd64" ./cmd/cutbridge
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build \
  -trimpath -ldflags="-s -w" \
  -o "$BUILD_DIR/CutBridge-arm64" ./cmd/cutbridge
popd >/dev/null

lipo -create \
  "$BUILD_DIR/CutBridge-amd64" \
  "$BUILD_DIR/CutBridge-arm64" \
  -output "$APP/Contents/MacOS/CutBridge"

chmod 755 "$APP/Contents/MacOS/CutBridge"
cp "$ROOT/macos/Info.plist" "$APP/Contents/Info.plist"
cp "$ROOT/macos/PkgInfo" "$APP/Contents/PkgInfo"
cp "$ROOT/macos/AppIcon.icns" "$APP/Contents/Resources/AppIcon.icns"
cp "$ROOT/LICENSE" "$APP/Contents/Resources/LICENSE.txt"

printf 'CutBridge %s\nBuilt: %s\n' "$VERSION" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  > "$APP/Contents/Resources/build-info.txt"

# Ad-hoc signing reduces local Gatekeeper friction but is not notarization.
codesign --force --deep --sign - "$APP"
codesign --verify --deep --strict "$APP"

ARCHIVE="$DIST_DIR/CutBridge_macOS_${VERSION}.zip"
ditto -c -k --sequesterRsrc --keepParent "$APP" "$ARCHIVE"
shasum -a 256 "$ARCHIVE" > "$ARCHIVE.sha256"

file "$APP/Contents/MacOS/CutBridge"
echo "已生成：$APP"
echo "已生成：$ARCHIVE"

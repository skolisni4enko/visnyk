#!/bin/bash
# Sign & notarize macOS .app / .dmg (requires Apple Developer ID)
# Usage: APPLE_ID=... APPLE_APP_PASSWORD=... TEAM_ID=... ./scripts/sign-macos.sh build/bin/Visnyk.app
set -euo pipefail
APP="${1:-build/bin/Visnyk.app}"
IDENTITY="${APPLE_IDENTITY:-Developer ID Application: Visnyk Team (TEAMID)}"
if [ -z "${APPLE_ID:-}" ]; then
  echo "[sign-macos] APPLE_ID not set — skip signing (Gatekeeper will warn)"
  exit 0
fi
echo "[sign-macos] Signing $APP with $IDENTITY"
codesign --deep --force --verify --verbose --sign "$IDENTITY" --options runtime "$APP"
echo "[sign-macos] Creating DMG"
DMG="build/bin/Visnyk-0.3.0.dmg"
hdiutil create -volname "Visnyk" -srcfolder "$APP" -ov -format UDZO "$DMG"
echo "[sign-macos] Notarizing $DMG"
xcrun notarytool submit "$DMG" --apple-id "$APPLE_ID" --password "$APPLE_APP_PASSWORD" --team-id "$TEAM_ID" --wait
xcrun stapler staple "$APP"
xcrun stapler staple "$DMG"
echo "[sign-macos] Done"

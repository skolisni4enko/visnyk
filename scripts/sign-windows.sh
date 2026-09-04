#!/bin/bash
# Sign Windows installer/exe with Authenticode (requires CSC_LINK env)
# Usage: CSC_LINK=path/to/cert.pfx CSC_KEY_PASSWORD=secret ./scripts/sign-windows.sh build/bin/visnyk.exe
set -euo pipefail
FILE="${1:-build/bin/visnyk.exe}"
if [ -z "${CSC_LINK:-}" ]; then
  echo "[sign-windows] CSC_LINK not set — skip signing (build remains unsigned)"
  exit 0
fi
if ! command -v osslsigncode &>/dev/null && ! command -v signtool &>/dev/null; then
  echo "[sign-windows] osslsigncode/signtool not found — install: apt install osslsigncode / choco install windows-sdk"
  exit 1
fi
echo "[sign-windows] Signing $FILE with $CSC_LINK"
if command -v osslsigncode &>/dev/null; then
  osslsigncode sign \
    -pkcs12 "$CSC_LINK" \
    -pass "${CSC_KEY_PASSWORD:-}" \
    -n "Visnyk" \
    -i "https://github.com/visnyk/visnyk" \
    -t "http://timestamp.digicert.com" \
    -in "$FILE" -out "${FILE}.signed" && mv "${FILE}.signed" "$FILE"
else
  signtool sign /fd SHA256 /f "$CSC_LINK" /p "${CSC_KEY_PASSWORD:-}" /tr http://timestamp.digicert.com /td SHA256 "$FILE"
fi
echo "[sign-windows] Done: $FILE"

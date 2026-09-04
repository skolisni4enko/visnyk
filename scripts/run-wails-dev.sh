#!/bin/bash
# Wrapper for `wails dev` (adjust paths to your Go/Wails install if needed).
SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
export PKG_CONFIG_PATH="$SCRIPT_DIR/.pkgconfig:/usr/lib/x86_64-linux-gnu/pkgconfig:/usr/lib/pkgconfig:/usr/share/pkgconfig:$PKG_CONFIG_PATH"
export PATH="$(go env GOPATH)/bin:$PATH"
cd "$SCRIPT_DIR"
exec wails dev "$@"

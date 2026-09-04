#!/bin/bash
# Wrapper for `go run` with WebKit pkg-config (adjust PKG_CONFIG_PATH to your system if needed).
SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
export PKG_CONFIG_PATH="$SCRIPT_DIR/.pkgconfig:/usr/lib/x86_64-linux-gnu/pkgconfig:/usr/lib/pkgconfig:/usr/share/pkgconfig:$PKG_CONFIG_PATH"
exec go run -tags=desktop,production "$@"

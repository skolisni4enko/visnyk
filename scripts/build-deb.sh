#!/bin/bash
# Build .deb via nfpm (preferred) or dpkg-deb fallback
set -euo pipefail
VERSION="${1:-0.1.0}"
echo "[build-deb] Building visnyk $VERSION"
# Ensure binary exists
if [ ! -f "build/bin/visnyk" ]; then
  echo "[build-deb] build/bin/visnyk not found — run: wails build -platform linux/amd64"
  echo "[build-deb] or: PKG_CONFIG_PATH=.pkgconfig:... go build -tags desktop,production -o build/bin/visnyk ."
  exit 1
fi
if command -v nfpm &>/dev/null; then
  echo "[build-deb] Using nfpm"
  nfpm pkg --packager deb --config build/nfpm.yaml --target "build/bin/visnyk_${VERSION}_amd64.deb"
else
  echo "[build-deb] nfpm not found — using dpkg-deb fallback"
  rm -rf build/deb
  mkdir -p build/deb/DEBIAN build/deb/usr/bin build/deb/usr/share/applications build/deb/usr/share/doc/visnyk
  cp build/bin/visnyk build/deb/usr/bin/visnyk
  # strip unstripped binary
  if command -v strip &>/dev/null; then
    strip --strip-unneeded build/deb/usr/bin/visnyk || true
  fi
  cp build/debian/visnyk.desktop build/deb/usr/share/applications/
  cp build/debian/DEBIAN/control build/deb/DEBIAN/
  # changelog and copyright for lintian (gzip -9, correct name for non-native is changelog.Debian.gz)
  if [ -f build/debian/changelog ]; then
    gzip -9c build/debian/changelog > build/deb/usr/share/doc/visnyk/changelog.Debian.gz
  else
    echo "visnyk (0.1.0-1) noble; urgency=low" | gzip -9c > build/deb/usr/share/doc/visnyk/changelog.Debian.gz
  fi
  cp LICENSE build/deb/usr/share/doc/visnyk/copyright
  sed -i "s/Version:.*/Version: $VERSION-1/" build/deb/DEBIAN/control
  chmod 0755 build/deb/DEBIAN
  chmod 0644 build/deb/usr/share/doc/visnyk/changelog.Debian.gz build/deb/usr/share/doc/visnyk/copyright
  chmod 0644 build/deb/usr/share/applications/visnyk.desktop
  chmod 0755 build/deb/usr/bin/visnyk
  # fix dir perms to 0755 (umask may give 0775)
  chmod 0755 build/deb/usr build/deb/usr/bin build/deb/usr/share build/deb/usr/share/applications build/deb/usr/share/doc build/deb/usr/share/doc/visnyk
  # fix ownership to root:root via fakeroot if available
  if command -v fakeroot &>/dev/null; then
    fakeroot dpkg-deb --build build/deb "build/bin/visnyk_${VERSION}_amd64.deb"
  else
    dpkg-deb --build build/deb "build/bin/visnyk_${VERSION}_amd64.deb"
  fi
fi
echo "[build-deb] Done: build/bin/visnyk_${VERSION}_amd64.deb"
ls -lh build/bin/*.deb

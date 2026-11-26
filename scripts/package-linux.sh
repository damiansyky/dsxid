#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
binary="$root/build/bin/DsxID"

if [[ ! -f "$binary" ]]; then
    echo "Missing $binary. Run a Linux Wails build first." >&2
    exit 1
fi

version="$(sed -n 's/^[[:space:]]*"productVersion": "\([^"]*\)".*/\1/p' "$root/wails.json" | head -n1)"
version="${version:-0.1.0}"

package_root="$root/build/package"
stage="$package_root/DsxID-linux-amd64"
archive="$package_root/DsxID-linux-amd64-$version.tar.gz"

# Keep a flat layout; install.sh expects the binary, icon, and desktop entry
# next to each other.
rm -rf "$stage"
mkdir -p "$stage"
install -m 755 "$binary" "$stage/DsxID"
install -m 644 "$root/build/appicon.png" "$stage/dsxid.png"
install -m 644 "$root/packaging/linux/dsxid.desktop" "$stage/dsxid.desktop"
install -m 755 "$root/packaging/linux/install.sh" "$stage/install.sh"
install -m 644 "$root/README.md" "$stage/README.md"
install -m 644 "$root/LICENSE" "$stage/LICENSE"

tar -C "$package_root" -czf "$archive" "DsxID-linux-amd64"
printf 'Created %s\n' "$archive"

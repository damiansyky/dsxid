#!/usr/bin/env sh
set -eu

data_home="${XDG_DATA_HOME:-$HOME/.local/share}"
bin_home="${XDG_BIN_HOME:-$HOME/.local/bin}"
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

install -d "$bin_home" "$data_home/applications" "$data_home/icons/hicolor/1024x1024/apps"
install -m 755 "$script_dir/DsxID" "$bin_home/DsxID"
install -m 644 "$script_dir/dsxid.png" "$data_home/icons/hicolor/1024x1024/apps/dsxid.png"
sed "s|^Exec=DsxID$|Exec=$bin_home/DsxID|; s|^TryExec=DsxID$|TryExec=$bin_home/DsxID|" \
    "$script_dir/dsxid.desktop" > "$data_home/applications/dsxid.desktop"
chmod 644 "$data_home/applications/dsxid.desktop"

if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database "$data_home/applications" >/dev/null 2>&1 || true
fi
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
    gtk-update-icon-cache -f -t "$data_home/icons/hicolor" >/dev/null 2>&1 || true
fi

printf 'Installed DsxID to %s\n' "$bin_home/DsxID"
printf 'DsxID should now appear in your application menu.\n'

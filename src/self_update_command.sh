tmp=$(mktemp)
info "Downloading $UPDATE_URL"
curl -fsSL "$UPDATE_URL" -o "$tmp" || die "Download failed"
bash -n "$tmp" || die "Downloaded script has syntax errors — not installed"
new_version=$(bash "$tmp" --version)
install -m 0755 "$tmp" "$INSTALL_PATH"
rm -f "$tmp"
ok "Installed postgres-manager $new_version to $INSTALL_PATH (was $version)"

#!/usr/bin/env bash

set -euo pipefail

repo_dir="$(realpath "$(dirname "${BASH_SOURCE[0]}")/../..")"
source_dir="$repo_dir/omarchy-plugin"
plugin_dir="${XDG_CONFIG_HOME:-$HOME/.config}/omarchy/plugins/timmo.twitch"
backup_dir="$(mktemp -d)"
had_plugin=false

cleanup() {
  trap - EXIT INT TERM HUP
  rm -rf "$plugin_dir"
  if [[ "$had_plugin" == true ]]; then
    mv "$backup_dir/plugin" "$plugin_dir"
  fi
  rm -rf "$backup_dir"
}

trap cleanup EXIT
trap 'exit 143' INT TERM HUP

omarchy plugin validate "$source_dir"
if [[ -d "$plugin_dir" ]]; then
  had_plugin=true
  mv "$plugin_dir" "$backup_dir/plugin"
fi

mkdir -p "$plugin_dir"
rsync -a \
  --include='*/' \
  --include='*.qml' \
  --include='manifest.json' \
  --exclude='*' \
  "$source_dir/" "$plugin_dir/"
sed -i "s|property string commandPath: \"twitch-notifications\"|property string commandPath: \"$repo_dir/twitch-notifications\"|" "$plugin_dir/Service.qml"
printf 'Published %s to %s\n' "$source_dir" "$plugin_dir"

while true; do sleep 3600; done

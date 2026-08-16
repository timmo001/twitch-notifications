#!/usr/bin/env bash

set -euo pipefail

restore_cwd=""
restore_cmd=()
production_pid=""

capture_production_process() {
  local pid exe

  while read -r pid; do
    exe="$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)"
    if [[ "$exe" != "/usr/bin/twitch-notifications" ]]; then
      continue
    fi

    mapfile -d '' -t restore_cmd <"/proc/$pid/cmdline"
    restore_cwd="$(readlink -f "/proc/$pid/cwd" 2>/dev/null || pwd)"
    production_pid="$pid"
    return
  done < <(pgrep -f '^/usr/bin/twitch-notifications( |$)' || true)
}

restore_production_process() {
  if ((${#restore_cmd[@]} == 0)); then
    return
  fi

  systemd-run --user --quiet --collect \
    --unit="twitch-notifications-restored-$$" \
    --working-directory="$restore_cwd" \
    "${restore_cmd[@]}"
}

cleanup() {
  trap - EXIT INT TERM HUP
  restore_production_process
}

trap cleanup EXIT
trap 'exit 143' INT TERM HUP

capture_production_process
if [[ -n "$production_pid" ]]; then
  kill -TERM "$production_pid" 2>/dev/null || true
  for _ in {1..20}; do
    if ! kill -0 "$production_pid" 2>/dev/null; then
      break
    fi
    sleep 0.1
  done
  kill -KILL "$production_pid" 2>/dev/null || true
fi

mise run build
while true; do
  set +e
  TWITCH_NOTIFICATIONS_SUPERVISED=1 ./twitch-notifications serve
  status=$?
  set -e

  if ((status == 0)); then
    break
  fi
  sleep 5
done

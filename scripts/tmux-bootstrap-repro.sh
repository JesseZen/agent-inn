#!/usr/bin/env bash
set -euo pipefail

readonly mode_fresh_outside="fresh-outside"
readonly mode_stale_host="stale-host"
readonly usage="usage: $(basename "$0") {$mode_fresh_outside|$mode_stale_host}"
readonly term_name="xterm-256color"
readonly windows_format='#{window_index}:#{window_name}'
readonly client_format='#{client_tty}|#{client_pid}|#{session_name}|#{window_index}'
readonly pane_format='#{window_index}|#{pane_id}|#{pane_start_command}'
readonly wait_attempts=50
readonly wait_sleep_seconds=0.2

mode="${1-}"
if [ "$#" -ne 1 ] || { [ "$mode" != "$mode_fresh_outside" ] && [ "$mode" != "$mode_stale_host" ]; }; then
  printf '%s\n' "$usage" >&2
  exit 64
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
binary_path="$repo_root/ainn"
if [ ! -x "$binary_path" ]; then
  printf 'missing executable: %s\nrun `make build` first.\n' "$binary_path" >&2
  exit 1
fi
if ! command -v tmux >/dev/null 2>&1; then
  printf 'tmux is required for %s\n' "$(basename "$0")" >&2
  exit 1
fi
if ! command -v script >/dev/null 2>&1; then
  printf 'script(1) is required for %s\n' "$(basename "$0")" >&2
  exit 1
fi
if ! command -v python3 >/dev/null 2>&1; then
  printf 'python3 is required for %s\n' "$(basename "$0")" >&2
  exit 1
fi

tmp_root="$(mktemp -d /tmp/ainn-tmux-repro.XXXXXX)"
config_dir="$tmp_root/c"
runtime_dir="$tmp_root/r"
state_dir="$tmp_root/s"
log_dir="$tmp_root/l"
tmux_tmpdir="$tmp_root/t"
trace_path="$tmp_root/tmux-trace.jsonl"
transcript_path="$tmp_root/bootstrap.typescript"
script_stdout_path="$tmp_root/script.stdout"
script_stderr_path="$tmp_root/script.stderr"
client_tty_path="$tmp_root/client.tty"
config_path="$config_dir/config.yaml"
socket_name="ar-$RANDOM-$$"
host_session="ar-host-$RANDOM-$$"
manager_port="$((20000 + ((RANDOM << 1) + RANDOM) % 30000))"
launcher_pid=""
launcher_status=""
trace_parse_failure=""
initiating_client_tty=""

tmux_cmd() {
  TMUX_TMPDIR="$tmux_tmpdir" tmux -f /dev/null -L "$socket_name" "$@"
}

parse_jsonl_trace() {
  if trace_parse_failure="$(TRACE_PATH="$trace_path" python3 - <<'PY'
import json
import os
import sys

path = os.environ["TRACE_PATH"]
required_keys = ("argv", "stdout", "stderr", "err", "duration_ms")

try:
    with open(path, encoding="utf-8") as handle:
        lines = handle.readlines()
except FileNotFoundError:
    print(f"trace file missing (trace_path: {path})")
    sys.exit(1)

if not lines:
    print(f"trace file empty (trace_path: {path})")
    sys.exit(1)

for line_number, line in enumerate(lines, start=1):
    try:
        record = json.loads(line)
    except json.JSONDecodeError as exc:
        print(
            f"trace JSONL line {line_number} failed to parse: {exc.msg} "
            f"at column {exc.colno} (trace_path: {path})"
        )
        sys.exit(1)
    if not isinstance(record, dict):
        print(f"trace JSONL line {line_number} is not an object (trace_path: {path})")
        sys.exit(1)
    missing_keys = [key for key in required_keys if key not in record]
    if missing_keys:
        print(
            f"trace JSONL line {line_number} missing keys: {', '.join(missing_keys)} "
            f"(trace_path: {path})"
        )
        sys.exit(1)
PY
  )"; then
    trace_parse_failure=""
    return 0
  fi
  return 1
}

show_diagnostics() {
  local reason="$1"
  local launcher_state="running"
  local windows_output=""
  local clients_output=""
  local pane_output=""

  if [ -n "$launcher_pid" ] && ! kill -0 "$launcher_pid" >/dev/null 2>&1 && [ -z "$launcher_status" ]; then
    if wait "$launcher_pid"; then
      launcher_status=0
    else
      launcher_status=$?
    fi
  fi
  if [ -n "$launcher_status" ]; then
    launcher_state="exited($launcher_status)"
  fi
  windows_output="$(tmux_cmd list-windows -t "$host_session" -F "$windows_format" 2>/dev/null || true)"
  clients_output="$(tmux_cmd list-clients -t "$host_session" -F "$client_format" 2>/dev/null || true)"
  pane_output="$(tmux_cmd list-panes -t "$host_session" -F "$pane_format" 2>/dev/null || true)"

  printf 'repro failed: %s\n' "$reason" >&2
  printf 'mode: %s\n' "$mode" >&2
  printf 'config_dir: %s\n' "$config_dir" >&2
  printf 'runtime_dir: %s\n' "$runtime_dir" >&2
  printf 'tmux_tmpdir: %s\n' "$tmux_tmpdir" >&2
  printf 'socket_name: %s\n' "$socket_name" >&2
  printf 'host_session: %s\n' "$host_session" >&2
  printf 'manager_port: %s\n' "$manager_port" >&2
  printf 'initiating_client_tty: %s\n' "${initiating_client_tty:-<missing>}" >&2
  printf 'trace_path: %s\n' "$trace_path" >&2
  printf 'transcript_path: %s\n' "$transcript_path" >&2
  printf 'launcher: %s\n' "$launcher_state" >&2
  printf 'windows:\n%s\n' "${windows_output:-<none>}" >&2
  printf 'clients:\n%s\n' "${clients_output:-<none>}" >&2
  printf 'panes:\n%s\n' "${pane_output:-<none>}" >&2
  if [ -f "$trace_path" ]; then
    printf 'trace_tail:\n' >&2
    tail -n 20 "$trace_path" >&2 || true
  else
    printf 'trace_tail:\n<missing>\n' >&2
  fi
  if [ -f "$script_stderr_path" ]; then
    printf 'script_stderr_tail:\n' >&2
    tail -n 20 "$script_stderr_path" >&2 || true
  fi
  if [ -f "$transcript_path" ]; then
    printf 'transcript_tail:\n' >&2
    tail -n 20 "$transcript_path" >&2 || true
  fi
}

cleanup() {
  local exit_code="$?"
  set +e
  if [ -n "${socket_name-}" ]; then
    TMUX_TMPDIR="$tmux_tmpdir" tmux -f /dev/null -L "$socket_name" kill-server >/dev/null 2>&1
  fi
  if [ -n "${launcher_pid-}" ] && [ -z "${launcher_status-}" ]; then
    wait "$launcher_pid" >/dev/null 2>&1
  fi
  exit "$exit_code"
}
trap cleanup EXIT

mkdir -p "$config_dir" "$runtime_dir" "$state_dir" "$log_dir" "$tmux_tmpdir"
cat >"$config_path" <<EOF
settings:
  state_dir: $state_dir
  log_dir: $log_dir
  terminal:
    host: tmux
    tmux:
      socket_name: $socket_name
      host_session: $host_session
      host_start_mode: main-tui-window
workers: {}
upstreams: {}
EOF

if [ "$mode" = "$mode_stale_host" ]; then
  if ! tmux_cmd new-session -d -s "$host_session" -n bootstrap "sh -c 'while :; do sleep 60; done'"; then
    show_diagnostics "failed to create stale-host tmux session"
    exit 1
  fi
  if ! tmux_cmd new-window -t "$host_session" -n old-bug "sh -c 'while :; do sleep 60; done'"; then
    show_diagnostics "failed to create stale-host old-bug window"
    exit 1
  fi
  if ! tmux_cmd kill-window -t "$host_session:0"; then
    show_diagnostics "failed to remove stale-host window 0"
    exit 1
  fi
  stale_windows="$(tmux_cmd list-windows -t "$host_session" -F '#{window_index}:#{window_name}')"
  if [ "$stale_windows" != "1:old-bug" ]; then
    show_diagnostics "stale-host precondition mismatch"
    exit 1
  fi
fi

TERM="$term_name" \
TMUX_TMPDIR="$tmux_tmpdir" \
XDG_RUNTIME_DIR="$runtime_dir" \
AINN_TMUX_DEBUG_LOG="$trace_path" \
AINN_CLIENT_TTY_PATH="$client_tty_path" \
AINN_BINARY_PATH="$binary_path" \
AINN_CONFIG_DIR="$config_dir" \
AINN_MANAGER_PORT="$manager_port" \
env -u TMUX -u TMUX_PANE -u AINN_TMUX_ROOT_CHILD \
script -q "$transcript_path" sh -lc 'tty > "$AINN_CLIENT_TTY_PATH"; exec "$AINN_BINARY_PATH" --config-dir "$AINN_CONFIG_DIR" --manager-port "$AINN_MANAGER_PORT"' >"$script_stdout_path" 2>"$script_stderr_path" &
launcher_pid="$!"

client_tty_seen=0
attempt=0
while [ "$attempt" -lt "$wait_attempts" ]; do
  if [ -s "$client_tty_path" ]; then
    initiating_client_tty="$(tr -d '\r\n' < "$client_tty_path")"
    if [ -n "$initiating_client_tty" ]; then
      client_tty_seen=1
      break
    fi
  fi
  if ! kill -0 "$launcher_pid" >/dev/null 2>&1; then
    break
  fi
  sleep "$wait_sleep_seconds"
  attempt="$((attempt + 1))"
done

if [ "$client_tty_seen" -ne 1 ]; then
  show_diagnostics "failed to capture initiating client tty from script(1)"
  exit 1
fi

trace_parsed=0
attempt=0
while [ "$attempt" -lt "$wait_attempts" ]; do
  if parse_jsonl_trace; then
    trace_parsed=1
    break
  fi
  if ! kill -0 "$launcher_pid" >/dev/null 2>&1; then
    break
  fi
  sleep "$wait_sleep_seconds"
  attempt="$((attempt + 1))"
done

if [ "$trace_parsed" -ne 1 ]; then
  show_diagnostics "${trace_parse_failure:-trace JSONL did not parse (trace_path: $trace_path)}"
  exit 1
fi

host_seen=0
attempt=0
while [ "$attempt" -lt "$wait_attempts" ]; do
  if tmux_cmd has-session -t "$host_session" >/dev/null 2>&1; then
    host_seen=1
    break
  fi
  if ! kill -0 "$launcher_pid" >/dev/null 2>&1; then
    break
  fi
  sleep "$wait_sleep_seconds"
  attempt="$((attempt + 1))"
done

if [ "$host_seen" -ne 1 ]; then
  show_diagnostics "bootstrap never created the isolated host session"
  exit 1
fi

postcondition_met=0
attempt=0
while [ "$attempt" -lt "$wait_attempts" ]; do
  windows_output="$(tmux_cmd list-windows -t "$host_session" -F "$windows_format" 2>/dev/null || true)"
  clients_output="$(tmux_cmd list-clients -t "$host_session" -F "$client_format" 2>/dev/null || true)"
  pane_output="$(tmux_cmd list-panes -t "$host_session" -F "$pane_format" 2>/dev/null || true)"

  if printf '%s\n' "$windows_output" | grep -q '^0:' \
    && printf '%s\n' "$clients_output" | awk -F '|' -v expected_tty="$initiating_client_tty" '$1 == expected_tty && $4 == "0" { found = 1 } END { exit found ? 0 : 1 }' \
    && printf '%s\n' "$pane_output" | awk -F '|' -v expected_path="$binary_path" '$1 == "0" && index($3, expected_path) && index($3, "AINN_TMUX_ROOT_CHILD=1") { found = 1 } END { exit found ? 0 : 1 }'; then
    postcondition_met=1
    break
  fi

  if ! kill -0 "$launcher_pid" >/dev/null 2>&1 && [ "$attempt" -ge 5 ]; then
    break
  fi
  sleep "$wait_sleep_seconds"
  attempt="$((attempt + 1))"
done

if [ "$postcondition_met" -ne 1 ]; then
  show_diagnostics "bootstrap did not reach window 0 with an attached isolated client"
  exit 1
fi

if ! parse_jsonl_trace; then
  show_diagnostics "${trace_parse_failure:-trace JSONL did not remain valid after bootstrap settled (trace_path: $trace_path)}"
  exit 1
fi

printf 'repro passed: %s\n' "$mode"
printf 'artifact_root: %s\n' "$tmp_root"
printf 'trace_path: %s\n' "$trace_path"

#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")" && pwd)"
cd "$root"

echo "Building tinyserve..."
go build -o "$root/tinyserve" ./cmd/tinyserve
echo "Building tinyserved..."
go build -o "$root/tinyserved" ./cmd/tinyserved

if command -v pgrep >/dev/null 2>&1; then
  pids="$(pgrep -f "cmd/tinyserved|/tinyserved" || true)"
  if [ -n "${pids}" ]; then
    echo "Stopping tinyserved: ${pids}"
    kill ${pids} || true
    for _ in {1..10}; do
      if ! pgrep -f "cmd/tinyserved|/tinyserved" >/dev/null 2>&1; then
        break
      fi
      sleep 0.3
    done
  fi
fi

echo "Starting tinyserved..."
nohup "$root/tinyserved" >/tmp/tinyserved.log 2>/tmp/tinyserved.err &
echo "tinyserved started (pid $!)"
echo "logs: tail -f /tmp/tinyserved.log"

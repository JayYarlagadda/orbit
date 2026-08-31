#!/usr/bin/env bash
# Validate tc netem availability and scenario contract before manual comparison.
# See docs/stretch/netem-validation.md.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
scenario="${1:-scenarios/examples/online-smoke.v1.json}"
scenario_path="$repo_root/$scenario"

if [[ ! -f "$scenario_path" ]]; then
  echo "scenario not found: $scenario_path" >&2
  exit 1
fi

if ! command -v tc >/dev/null 2>&1; then
  echo "tc not found; install iproute2 or run on Linux/WSL with netem support" >&2
  exit 1
fi

echo "==> scenario contract"
(cd "$repo_root" && go run ./cmd/scenario-check "$scenario")

echo "==> netem profile (manual apply)"
cat <<'EOF'
sudo tc qdisc add dev lo root netem delay 500ms loss 5%
# run scenario-run, then:
sudo tc qdisc del dev lo root
EOF

echo "Document results under docs/results/netem/ when complete."

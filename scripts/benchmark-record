#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
if [[ -n $(git status --porcelain) ]]; then
  printf 'Commit or stash changes before recording benchmarks.\n' >&2
  exit 1
fi

commit=$(git rev-parse HEAD)
mkdir -p benchmarks
output="benchmarks/$commit.txt"
temporary=$(mktemp "benchmarks/$commit.XXXXXX")
trap 'rm -f "$temporary"' EXIT

# Override these settings for a focused comparison. Use identical settings for
# every commit being compared. "cold" clears only the application patch cache.
args=(./internal/review -run '^$'
  -bench "${BENCHMARK_FILTER:-Benchmark(Snapshot|Status)$}"
  -benchmem -benchtime="${BENCHMARK_TIME:-1x}"
  -count="${BENCHMARK_COUNT:-10}" -timeout=30m)
{
  printf '# commit: %s\n' "$commit"
  printf '# toolchain: %s\n' "$(go version)"
  printf '# command: go test'; printf ' %q' "${args[@]}"; printf '\n'
  go test "${args[@]}"
} | tee "$temporary"
if ! grep -q '^Benchmark' "$temporary"; then
  printf 'No benchmarks matched; no record saved.\n' >&2
  exit 1
fi
mv "$temporary" "$output"
printf 'Saved %s\n' "$output" >&2

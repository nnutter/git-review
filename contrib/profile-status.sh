#!/usr/bin/env bash
# Generate a wall-clock flame graph without changing project dependencies.
set -euo pipefail

root=$(git rev-parse --show-toplevel)
output="$(git rev-parse --absolute-git-dir)/status-profile"
work=$(mktemp -d "${TMPDIR:-/tmp}/git-review-profile.XXXXXX")
trap 'rm -rf "$work"' EXIT
mkdir -p "$output"
export PROFILE_ROOT="$root" PROFILE_WORK="$work" PROFILE_OUTPUT="$output"

# Use the real command entry point through an overlay. fgprof includes time
# waiting for Git, which a CPU profile would omit.
cp "$root/go.mod" "$work/profile.mod"
cp "$root/go.sum" "$work/profile.sum"
cat > "$work/main.go" <<'GO'
package main

import (
 "context"
 "fmt"
 "os"
 "time"

 "github.com/felixge/fgprof"
 "github.com/nnutter/git-review/internal/cli"
)

func main() {
 f, err := os.Create(os.Getenv("STATUS_PROFILE"))
 if err != nil { panic(err) }
 stop := fgprof.Start(f, fgprof.FormatFolded)
 start := time.Now()
 err = cli.Execute(context.Background(), os.Args[1:], ".", os.Stdin, os.Stdout, os.Stderr, version)
 elapsed := time.Since(start)
 if profileErr := stop(); profileErr != nil { panic(profileErr) }
 if closeErr := f.Close(); closeErr != nil { panic(closeErr) }
 fmt.Fprintf(os.Stderr, "Status elapsed: %s\n", elapsed)
 if err != nil { os.Exit(1) }
}
GO

uvx python - <<'PY'
import json
import os
from pathlib import Path
import subprocess

root = Path(os.environ["PROFILE_ROOT"])
work = Path(os.environ["PROFILE_WORK"])
(work / "overlay.json").write_text(json.dumps({"Replace": {str(root / "main.go"): str(work / "main.go")}}))
if not os.environ.get("STATUS_PROFILE_REPOSITORY"):
    count = int(os.environ.get("STATUS_PROFILE_COMMITS", "350"))
    if count < 1:
        raise SystemExit("STATUS_PROFILE_COMMITS must be positive")
    repo = work / "fixture"
    subprocess.run(["git", "init", "-q", "-b", "main", str(repo)], check=True)
    stream = "commit refs/heads/main\ncommitter Profile <profile@example.com> 1000000000 +0000\ndata 4\nBase\n\n"
    for i in range(count):
        message = f"Feature {i}\n\nChange-Id: I{i:040x}\n"
        stream += f"commit refs/heads/feature\ncommitter Profile <profile@example.com> 1000000000 +0000\ndata {len(message)}\n{message}"
        if i == 0:
            stream += "from refs/heads/main\n"
        stream += f"M 100644 inline file-{i}\ndata 8\ncontent\n\n"
    subprocess.run(["git", "fast-import", "--quiet"], cwd=repo, input=stream.encode(), check=True)
    subprocess.run(["git", "symbolic-ref", "HEAD", "refs/heads/feature"], cwd=repo, check=True)
    subprocess.run(["git", "branch", "--set-upstream-to=main"], cwd=repo, check=True)
PY

cd "$root"
go get -modfile="$work/profile.mod" github.com/felixge/fgprof@v0.9.5
go build -modfile="$work/profile.mod" -overlay="$work/overlay.json" -o "$work/git-review" .
# Pin the renderer so later upstream changes cannot alter this task.
curl -fsSL https://raw.githubusercontent.com/brendangregg/FlameGraph/41fee1f99f9276008b7cd112fca19dc3ea84ac32/flamegraph.pl -o "$work/flamegraph.pl"

repo=${STATUS_PROFILE_REPOSITORY:-$work/fixture}
cd "$repo"
printf 'Warming the patch cache...\n'
STATUS_PROFILE="$work/warmup.folded" "$work/git-review" status > /dev/null
printf 'Profiling status with a warm patch cache...\n'
STATUS_PROFILE="$output/all.folded" "$work/git-review" status > "$output/status.txt" 2> "$output/timing.txt"

uvx python - <<'PY'
import html
import os
from pathlib import Path

output = Path(os.environ["PROFILE_OUTPUT"])
lines = (output / "all.folded").read_text().splitlines()
main = [line for line in lines if ";main.main;" in line]
if not main:
    raise SystemExit("No main-goroutine samples captured. Try a larger commit range.")
(output / "main.folded").write_text("\n".join(main) + "\n")
repo = os.environ.get("STATUS_PROFILE_REPOSITORY")
label = f"Repository: {repo}" if repo else f"Synthetic repository: {os.environ.get('STATUS_PROFILE_COMMITS', '350')} independent commits, one new file per commit, no saved review history."
timing = (output / "timing.txt").read_text()
(output / "index.html").write_text(f'''<!doctype html>
<html lang="en"><meta charset="utf-8"><title>git-review status profile</title>
<style>body{{font:16px system-ui;margin:24px}}p{{max-width:1000px;line-height:1.5}}object{{width:100%;height:900px}}</style>
<h1>git-review status: warm-cache wall-clock profile</h1>
<p>{html.escape(label)}</p><pre>{html.escape(timing)}</pre>
<p>Only the main goroutine is shown. Width represents elapsed samples, including waits, not CPU use.
Git subprocess internals are not sampled. Background goroutines are excluded to avoid counting concurrent waits more than once.</p>
<p>Click frames to zoom. Press Ctrl-F to search. <a href="flamegraph.svg">Open SVG</a> · <a href="main.folded">Main stacks</a> · <a href="all.folded">All goroutines</a> · <a href="status.txt">Command output</a></p>
<object data="flamegraph.svg" type="image/svg+xml"></object></html>''')
PY
perl "$work/flamegraph.pl" --title 'git-review status: warm cache' --subtitle 'Main goroutine, wall-clock samples (~99 Hz)' --countname samples --width 1600 "$output/main.folded" > "$output/flamegraph.svg"
printf 'Profile: %s/index.html\n' "$output"

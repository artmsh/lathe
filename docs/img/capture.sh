#!/usr/bin/env bash
# Regenerates the README screenshots in docs/img/ from the code in this checkout.
#
# Builds lathe, stores four demo tutorials in a throwaway HOME, runs `lathe serve` on a free
# loopback port and photographs it with headless Chrome. Nothing touches your ~/.lathe or
# your browser profile. The demo tutorials below are fixtures written for the screenshots;
# the onboarding guide quotes this repository at HEAD.
#
# Needs: go, python3, curl, ImageMagick 7 (`magick`), Google Chrome (or CHROME=/path).
# Usage: docs/img/capture.sh
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
out="$root/docs/img"
chrome=${CHROME:-"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
work=$(mktemp -d)
pid=""
cleanup() { [ -n "$pid" ] && kill "$pid" 2>/dev/null; rm -rf "$work"; }
trap cleanup EXIT

(cd "$root" && go build -o "$work/lathe" .)
commit=$(git -C "$root" rev-parse HEAD)
# Only lathe gets the throwaway HOME: headless Chrome on macOS hangs with a fresh empty HOME.
mkdir -p "$work/home" "$work/src"
lathe() { HOME="$work/home" "$work/lathe" "$@"; }

# --- fixtures ---------------------------------------------------------------------------

d="$work/src/lock-free-ring-buffer"; mkdir -p "$d"
cat > "$d/part-01.md" <<'EOF'
# A single-producer ring buffer

By the end of this part, you'll have a fixed-size ring buffer that one goroutine fills and another drains, with no mutex and no channel, and a test that proves it never loses a value.

A ring buffer is an array with two counters chasing each other around it: the producer advances `tail` after writing a slot, the consumer advances `head` after reading one. With exactly one goroutine on each side, each counter has a single writer, so the only synchronization you need is a release store on one side and an acquire load on the other, which is what [`sync/atomic`](https://pkg.go.dev/sync/atomic) gives you under the [Go memory model](https://go.dev/ref/mem).

```go
type Ring[T any] struct {
	buf  []T
	mask uint64
	head atomic.Uint64 // next slot to read; written only by the consumer
	tail atomic.Uint64 // next slot to write; written only by the producer
}
```

> [!ASIDE]
> The `mask` field only works because the size is a power of two: for those sizes `i & (n-1)` equals `i % n`, and an AND is cheaper than a division.

## Push and Pop

```go
func (r *Ring[T]) Push(v T) bool {
	tail := r.tail.Load()
	if tail-r.head.Load() == uint64(len(r.buf)) {
		return false // full
	}
	r.buf[tail&r.mask] = v
	r.tail.Store(tail + 1) // publish the slot
	return true
}
```

> [!DESIGN-NOTE]
> The counters never wrap back to zero; only their masked value does. Because `tail - head` is then the fill level, a full buffer and an empty one look different without sacrificing a slot.

```mermaid
flowchart LR
  P[Producer] -->|write slot, store tail| R[(buf)]
  R -->|load tail, read slot, store head| C[Consumer]
```

## Checkpoint

> [!PREDICT]
> With a size of 4, how many calls to `Push` succeed before the first `false`?

```bash
go test -run TestFull -v
```

## Exercises

- [ ] Add `Len()` and explain why its result can be stale by the time the caller reads it.
- [ ] Make `Pop` return the zero value of `T` together with `false` when the buffer is empty.
- [ ] Run the test under `-race` with the `Store` replaced by a plain assignment and read the report.

## Sources

1. [The Go Memory Model](https://go.dev/ref/mem) — defines the synchronizes-before edge an atomic store and load create.
2. [sync/atomic](https://pkg.go.dev/sync/atomic) — the `atomic.Uint64` type used for both counters.
EOF
cat > "$d/part-02.md" <<'EOF'
# Many producers with compare-and-swap

By the end of this part, several goroutines can push into the same ring without a mutex.

A second producer breaks the single-writer property of `tail`, so `Push` has to claim a slot with `CompareAndSwap` before writing it.

## Exercises

- [ ] Count how often the CAS loop retries under four producers.

## Sources

1. [sync/atomic](https://pkg.go.dev/sync/atomic) — `Uint64.CompareAndSwap`.
EOF
cat > "$d/part-03.md" <<'EOF'
# Measuring it against a channel

By the end of this part, you'll have benchmarks comparing the ring with a buffered channel of the same capacity.

## Exercises

- [ ] Vary the capacity from 8 to 4096 and plot ns/op.

## Sources

1. [testing: Benchmarks](https://pkg.go.dev/testing#hdr-Benchmarks) — the `b.Loop` benchmark form.
EOF

d="$work/src/forth-in-c"; mkdir -p "$d"
cat > "$d/index.md" <<'EOF'
# A Forth interpreter in C

By the end of this tutorial, you'll have a Forth that reads words from stdin, keeps a data stack, and lets you define new words with `:` and `;`.

## Exercises

- [ ] Add `IF ... THEN` to the compiler.

## Sources

1. [Starting FORTH](https://www.forth.com/starting-forth/) — the dictionary and stack model.
EOF

d="$work/src/digital-synth-zig"; mkdir -p "$d"
printf '# Oscillators\n\nBy the end of this part, you will hear a sine wave.\n\n## Sources\n\n1. [Zig 0.13.0 documentation](https://ziglang.org/documentation/0.13.0/) — language reference.\n' > "$d/part-01.md"
printf '# Envelopes\n\nBy the end of this part, notes fade in and out.\n\n## Sources\n\n1. [Zig 0.13.0 documentation](https://ziglang.org/documentation/0.13.0/) — language reference.\n' > "$d/part-02.md"

# Onboarding guide over this repository, anchored at HEAD with a real excerpt.
d="$work/src/onboarding-lathe"; mkdir -p "$d"
file=internal/serve/server.go
first=$(grep -nF 'mux.HandleFunc("GET /{$}"' "$root/$file" | cut -d: -f1)
[ -n "$first" ] || { echo "capture.sh: anchor line not found in $file" >&2; exit 1; }
last=$((first + 2))
{
  printf '# What happens when you open a tutorial\n\n'
  printf 'By the end of this guide, you can trace a browser request from the route table to the rendered page.\n\n'
  printf '```go path=%s lines=%s-%s\n' "$file" "$first" "$last"
  sed -n "${first},${last}p" "$root/$file" | sed 's/^\t//'
  printf '```\n'
} > "$d/part-01.md"

# --- store through the real CLI -----------------------------------------------------------

lathe store "$work/src/lock-free-ring-buffer" --tags go,concurrency,lock-free --tool go:1.25 \
  --source https://go.dev/ref/mem --source https://pkg.go.dev/sync/atomic \
  --source https://pkg.go.dev/testing >/dev/null
lathe verify-result lock-free-ring-buffer --status verified >/dev/null
lathe store "$work/src/forth-in-c" --tags c,interpreters \
  --source https://www.forth.com/starting-forth/ >/dev/null
lathe store "$work/src/digital-synth-zig" --tags zig,audio,dsp --tool zig:0.13.0 \
  --source https://ziglang.org/documentation/0.13.0/ >/dev/null
lathe verify-result digital-synth-zig --status skipped --error "zig not installed" >/dev/null
lathe store "$work/src/onboarding-lathe" --kind onboarding --repo https://github.com/artmsh/lathe \
  --repo-commit "$commit" --repo-path "$root" --tags go,onboarding >/dev/null

# --- serve and photograph -----------------------------------------------------------------

port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')
url="http://127.0.0.1:$port"
HOME="$work/home" "$work/lathe" serve --port "$port" --no-open >"$work/serve.log" 2>&1 &
pid=$!
for _ in $(seq 50); do curl -fsS "$url/" >/dev/null 2>&1 && break; sleep 0.2; done
curl -fsS "$url/" >/dev/null || { cat "$work/serve.log" >&2; exit 1; }

limit=$(command -v timeout >/dev/null && echo "timeout 60" || true)
shot() { # shot <url> <out.png> <height> <virtual-time-ms> [extra chrome flags...]
  local target=$1 png=$2 height=$3 budget=$4; shift 4
  $limit "$chrome" --headless=new --hide-scrollbars --user-data-dir="$work/chrome" \
    --window-size=1280,"$height" --force-device-scale-factor=2 --virtual-time-budget="$budget" \
    "$@" --screenshot="$work/$png" "$target" >/dev/null 2>&1
  # 2x capture -> 1440 px wide, 256-colour palette (displayed at width=720 in the README).
  magick "$work/$png" -resize '1440x>' -strip -colors 256 -define png:compression-level=9 "PNG8:$out/$png"
}

# The list page runs 10 s of virtual time so the one-time live-mode nudge (7 s) is gone and
# marked seen in the profile. Reading pages stop at 2 s: after 3 s of dwell the page
# auto-saves progress and draws the saved-progress marker at 0 %.
shot "$url/" library-light.png 800 10000
shot "$url/lock-free-ring-buffer/" reading-light.png 960 2000
shot "$url/lock-free-ring-buffer/" reading-dark.png 960 2000 --force-dark-mode --blink-settings=preferredColorScheme=0
ls -l "$out"/library-light.png "$out"/reading-light.png "$out"/reading-dark.png

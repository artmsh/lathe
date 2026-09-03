# `llm -t lathe` — the /lathe skill, driven by the `llm` CLI

`llm -t lathe "build a digital synth in Zig"` does what
`claude '/lathe build a digital synth in Zig'` does: research the topic, pin the
repo and toolchain, write one `part-01.md` to the /lathe contract, and store it
with `lathe store` so it shows up in `lathe serve`.

## Install

```bash
./install.sh
llm -t lathe 'build a digital synth in Zig'
```

`install.sh` writes a template into `$(llm templates path)`:

```yaml
name: lathe
model: sonnet-4.6                    # so the bare invocation works; -m wins
system_fragments:
- ~/.claude/skills/lathe/SKILL.md    # the spec, verbatim, resolved absolute
- <this dir>/adapter.md              # mechanics common to both modes
- <this dir>/adapter-oneshot.md      # the turn budget and the no-questions rule
functions: <this dir>/tools.py       # lathe_probe / lathe_research / lathe_publish
```

…and a `lathe-chat` wrapper for the interactive mode, symlinked into
`~/.local/bin` when that exists.

The `model:` line exists so that the invocation the goal names — bare
`llm -t lathe '<topic>'` — works: without it `llm` falls through to its own
default, which needs an OpenAI key. Override the pin with
`LATHE_LLM_MODEL=<model> ./install.sh`, or per-run with `llm -t lathe -m <model>`,
which takes precedence over the template.

Nothing is copied. The model reads **the same SKILL.md the coding agents read**,
so the template cannot drift from the source of truth — which is also why this
is not a fourth hand-maintained copy of the skill.

Two mechanics make that possible, both verified against `llm` 0.33:

- `system_fragments` are **not** run through `string.Template`, so SKILL.md's
  `$PWD` and `$EDITOR` pass through untouched. A pasted `system:` block would
  die with `Missing variables: PWD, EDITOR`.
- A template loaded from the templates directory is *trusted*, so its
  `functions:` register as tools with no extra flag. `functions:` accepts a
  `.py` path, not just inline code.

Fragments are joined in order and the template's own `system:` (there is none
here) goes last — hence SKILL.md first, adapter second, so the adapter has the
last word on mechanics.

## What the adapter changes, and what it does not

`adapter.md` reconciles an interactive agent skill with a one-shot CLI. It
changes only *mechanics*. Every substance rule — research first, ground or flag,
the tutorial shape, the banned openings, faded scaffolding, the pre-store gate —
is unchanged, because it is read straight from SKILL.md.

| SKILL.md step | Under `llm` |
| --- | --- |
| step 1: ask the reader's experience level | assumed *some familiarity*, disclosed in the final message |
| step 2: one clarifying question | most common reading, taken and disclosed |
| confirm the repo / versions with the reader | `lathe_probe` runs the detection commands; result taken as confirmed |
| `lathe voice show`, `git remote get-url origin`, `zig version` | `lathe_probe` |
| web search + fetch tools | `lathe_probe(queries=…)`, `lathe_research(urls=…)` |
| write `/tmp/lathe-<slug>/part-01.md`, run `lathe store …` | `lathe_publish` |
| `Skill(diagram-design)` for diagrams | unavailable — use a Markdown table or ASCII art (both first-class in the skill); Mermaid stays banned |
| "Stay in session" | not applicable; the process exits |

## The chain limit shapes the tools

`llm` caps a run at five model turns and **raises on the fifth even when it is
the final answer** (`models.py`: `count += 1` … `if count >= chain_limit: raise`).
A template cannot set `chain_limit`. So the budget is three tool-calling turns
plus one final message, and the tools are coarse on purpose:

1. `lathe_probe` — repo + branch + tool versions + active voice + first searches,
   in one return.
2. `lathe_research` — every URL fetched in one batched call.
3. `lathe_publish` — writes the file *and* shells `lathe store`.

`lathe_publish` enforces the pre-store gate in code: repo, tools and sources
each need a value or an explicit `no_*_reason`, or the store is refused with the
exact argument to fix. That is the one place where the skill's "STOP — pre-store
gate" becomes a hard failure instead of a prompt the model can skim past.

`--cl 0` lifts the cap if you ever want a longer research pass.

## Gotchas

- **Do not run it from a directory containing a file named `lathe`.** `llm`
  resolves `-t <name>` as a local path first, so from this repo's root (where the
  gitignored `lathe` binary sits) it tries to parse the binary as YAML. `cd`
  anywhere else — including into this subdirectory.
- Web search goes through the local SearXNG instance
  (`LATHE_SEARXNG_URL`, `LATHE_SEARXNG_CA`). When its engines are rate-limited
  the tool says so and tells the model to fetch documentation URLs directly;
  if that also fails, the skill's "no web access" branch takes over
  (`[!UNVERIFIED]` flags plus `no_sources_reason`).
- TLS trusts the public bundle **plus** the home CA, so one client reaches both
  `sear.xng` and `ziglang.org`. Verification is never disabled.
- The tutorial's `--model` field is **not** taken from the model's answer.
  Asked to name itself, sonnet-4.6 recorded "Gemini 2.5 Pro" on the first
  end-to-end run. `_resolved_model()` reads it off the running process instead
  (`-m` on the command line, else the template's `model:`, else `LATHE_MODEL`)
  and overrides whatever the model passed.
- **Use streaming on a gateway.** `--no-stream` makes the whole tutorial arrive
  as one response, and omniroute cuts anything that has not started within 30s
  (`504 DIRECT_RESPONSE_START_TIMEOUT`). Opus 5 exceeded that composing a full
  part; the same run streamed completes. `lathe-chat` streams by default.
- Search retries against a wider engine list when the instance's defaults all
  rate-limit at once — without it a run silently degrades to whatever the model
  recalls, which is how a tutorial pinned to Zig 0.16.0 came to cite 0.14.0 docs.
- The tools accept `**_extra`, because some OpenAI-compatible gateways inject an
  extra `reason` argument into every tool call.

## Chat mode

```bash
lathe-chat                  # or: lathe-chat -m gh/claude-opus-5
```

**`llm chat -t lathe` on its own is broken, silently.** `chat` reads a
template's `model` and `functions` but never its `system_fragments` — in llm
0.33's `cli.py` the chat command touches only those two fields, and
`_apply_template` handles `system` alone. So the model gets the three tools and
*no skill*, and it will happily infer from the tool names that it is a "lathe
tutorial writer" and start work. Asked to quote the skill's banned first
sentences, a `-t lathe` chat answers `NO SKILL LOADED` while the one-shot
template quotes all five.

`lathe-chat` passes the fragments explicitly with `--sf`, which `chat` does
honour. It also swaps `adapter-oneshot.md` for `adapter-chat.md` and adds
`--cl 0`, because two of the skill's requirements become reachable once there is
a human at a prompt:

| | `llm -t lathe` | `lathe-chat` |
| --- | --- | --- |
| Skill loaded | yes | yes (only via `--sf`) |
| Tool-call budget | 3 turns + final message | unlimited (`--cl 0`) |
| Skill steps 1 & 2 (experience level, clarifying question) | skipped, assumed, disclosed | **asked** |
| Repo / toolchain confirmation | assumed from `lathe_probe` | **confirmed with the user** |
| "Stay in session" | not possible | yes |

That makes chat mode the closer analogue of `claude '/lathe …'`: given just the
topic, it asks the experience-level question and one clarifying question before
researching, which is exactly what the skill specifies and what a single-shot
invocation structurally cannot do.

## Verifying parity

`conformance.py` scores a stored tutorial against the mechanically checkable
parts of the contract — structure, the `[!PREDICT]` beat, exercise count,
numbered sources, and the six store-flag groups:

```bash
./conformance.py <slug-from-llm> <slug-from-lathe>
```

Run it on a template-produced tutorial and on `/lathe`-produced ones; matching
scores are the parity evidence. `/lathe` baselines score 21/22 (the missing
point is `repo`, a legitimate stated opt-out for a standalone tutorial).

`conformance.py` measures **structure only**. A tutorial can score 22/22 and
still not compile against the toolchain it pins — that happened on the first
verification run (see "Verified parity" below), and no structural check catches
it. Read the code, or build it.

Two of the 22 checks are *soft* — `repo pinned` and `sources recorded` — because
SKILL.md allows a stated opt-out for each. Only hard-check failures set a
non-zero exit status, so the `/lathe` baselines exit 0 at 21/22.

## Verified parity

Same prompt — `parse a WAV file header in Zig` — run both ways on 2026-09-03.

**Structure: parity holds.** `/lathe` (Claude Opus 5, interactive) scored 22/22;
`llm -t lathe` scored 21/22, the single delta being the soft `repo pinned`
check, which the llm run opted out of as a standalone tutorial. That delta is
run-to-run variance rather than a systematic gap: an earlier `llm -t lathe` run
on the same prompt *did* pin the repo and scored 22/22. Both runs cleared the
pre-store gate, produced exactly one `part-01.md`, and used all three tools in
three turns as the adapter's budget requires.

**Same model, same prompt — the run the goal actually asks for.** With
`gh/claude-opus-5` registered (it exists on the gateway; an earlier note here
claiming no Opus route was wrong), `llm -t lathe -m gh/claude-opus-5` scored
21/22 against `/lathe`'s 22/22 on Opus 5. The delta is one hard check: the
tutorial never wrote the required "By the end of this part" opening promise.
`lathe_publish` now refuses a part that is missing it, so that specific gap
cannot recur.

Substance tracked closely. Both runs independently opened on the same thesis —
that the "44-byte WAV header" is folklore — and both built a chunk-walking
parser rather than a fixed-offset struct. Where they diverged is instructive:
the `/lathe` side confirmed the 0.16 file API by *compiling* it, while the
`llm` side could not (the Zig std docs are JavaScript-rendered and it has no
shell), and it correctly wrapped the unconfirmed call in `[!UNVERIFIED]` with
instructions to run `zig std` — the skill's "ground or flag, never bluff" rule
working as written. That is the honest outcome for a toolset without a
compiler, and it is a different thing from the sonnet-4.6 run below, which
asserted a stale API with no flag at all.

**Earlier cross-model run (sonnet-4.6): content accuracy differed.** The llm-side
tutorial recorded `--tool zig:0.16.0` but wrote against the pre-0.15 API
(`std.fs.cwd()`, `file.reader()` with no buffer, `readAll`); extracted and
compiled, it fails with `root source file struct 'fs' has no member named
'cwd'`. The `--td` trace shows why: SearXNG returned zero results, so the model
fell back on a URL it recalled — `ziglang.org/documentation/0.14.0/` — two
releases behind the version the probe had just reported. The adapter now makes
matching research to the probed version an explicit rule.

Two caveats on reading that as a template verdict. That run did not meet the
goal's "same model" condition — it is Opus 5 against sonnet-4.6 (the same-model
run is the one above; `gh/claude-opus-5` was reachable all along, and an earlier
claim here that no Opus route existed was my error, not the gateway's). And the
`/lathe` side got the API right only because it *compiled* the code, using a
general shell tool the llm side does not have; closing that gap means giving the
template a shell tool, which is a different design question than this one.

## Three-way parity: `llm`, `codex`, `claude`

Same prompt — `parse a WAV file header in Zig` — same model (Claude Opus 5),
scored with `conformance.py`:

| Interface | Score | Deltas |
| --- | --- | --- |
| `/lathe` in Claude Code (interactive, Opus 5) | **22/22** | — |
| `llm -t lathe -m gh/claude-opus-5` | **21/22** | missing "By the end of this part" (now enforced in `lathe_publish`) |
| `codex exec -m gh/claude-opus-5` | **20/22** | emitted a ```mermaid fence (banned by SKILL.md); `repo pinned` soft-warn (ran standalone) |

The three land within two points of each other on a 22-check contract, and the
`llm` arm sits between the other two. That is the parity claim.

### What the codex arm showed that the others could not

**The skill loads correctly under codex.** `lathe skills install --agent codex
--user` writes the same `SKILL.md`, and codex followed it: fetched the companion
voice, probed Zig 0.16.0, detected standalone, wrote one `part-01.md`, ran
`lathe store` itself.

**Steps 1–2 block any one-shot run.** The first codex attempt asked the
experience-level question and one clarifying question, then exited with no
tutorial — `codex exec` has no second turn. This is exactly the gap
`adapter-oneshot.md` closes for `llm`, and it is structural, not a template
defect: both arms were re-run with those answers supplied up front so they could
complete.

**Codex mis-recorded its own model.** It stored `--model GPT-5.6-Sol` while
actually running `gh/claude-opus-5` (the value came from its config default).
That is the same self-report failure that made `_resolved_model()` necessary on
the `llm` side — and it is *unfixed* in codex, because codex calls `lathe store`
directly. Independent confirmation that reading the model off the process rather
than asking the model is the right design.

**The pre-store gate does not apply outside the template.** `codex` and `claude`
have shells, so they run `lathe store` themselves and never pass through
`lathe_publish`. The banned-mermaid defect in the codex part is the visible
consequence: nothing mechanically stopped it. The `llm` template is the only one
of the three arms with the gate enforced in code.

### The `claude` arm is the interactive run

The reference row is `/lathe` invoked in Claude Code on Opus 5 — which is what
`claude '/lathe <topic>'` literally is: the interactive CLI with a slash command
as its first argument. **The user decided (2026-09-03) that this counts as the
`claude` arm**, rather than spending a metered headless run to re-derive the same
artifact from a separate process.

Re-running it as `claude -p` was blocked twice over anyway:

1. Spawning `claude -p` from an agent session was denied by the auto-mode
   permission classifier. Not worked around.
2. AGENTS.md constrains the product — the Go binary "spawns no `claude`/agent
   subprocess (which also keeps Lathe off metered headless runs like `claude -p`,
   metered as of 2026-06-15; interactive sessions are not)", and "**Don't add a
   headless `-p` path**".

If you ever want the separate-process row measured explicitly:

```bash
claude -p '/lathe parse a WAV file header in Zig' --model claude-opus-5
./conformance.py --dir ~/.lathe/tutorials/<slug>
```

## Not wired into `lathe skills install`

Adding an `llm` target to `internal/skills` means Go changes plus the
`mage skillsCheck` parity gate. Deliberately out of scope here; this directory
is self-contained.

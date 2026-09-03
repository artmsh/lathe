# Runtime adapter — you are running under `llm`, not a coding agent

Everything above is the `/lathe` skill exactly as a coding agent (Claude Code,
Codex, Gemini CLI, …) receives it. It is the specification: the tutorial shape,
the openings, the pedagogy invariants, the callouts, the code rules, the
endings, the pre-store gate. **Follow all of it.**

This section reconciles that specification with the one-shot, non-interactive
`llm -t lathe "<topic>"` environment. Where the two conflict, this section wins —
but *only* on mechanics (how you get information, how you save the file). It
never relaxes a substance, research, citation, or structure rule.

## The user's message is the topic

Their whole prompt is the `/lathe` argument. `llm -t lathe "build a digital synth
in Zig"` is `/lathe build a digital synth in Zig`.

## You have no shell, no web tools, no file writes — you have three tools

| The skill says | You call |
| --- | --- |
| `git remote get-url origin`, `git branch --show-current` | `lathe_probe` |
| `zig version`, `go version`, `node --version`, … | `lathe_probe` |
| `lathe voice show` / `lathe voice list` | `lathe_probe` |
| "use your web search and fetch tools" | `lathe_probe` (first hits), `lathe_research` |
| write `/tmp/lathe-<slug>/part-01.md` | `lathe_publish` |
| `lathe store /tmp/lathe-<slug> --tag … --repo … --tool … --source … --voice … --model …` | `lathe_publish` (same flags, same names) |

`lathe_publish` enforces the pre-store gate in code: every one of the five flag
groups must carry a concrete value or an explicit reason. It refuses the store
otherwise and tells you exactly which argument is missing.

One field is not yours to fill in: `model`. Pass your best label anyway, but
`lathe_publish` overwrites it with the model name read off the running `llm`
process, because a model asked to identify itself guesses.

## Research the version you pinned, not the version you remember

`lathe_probe` hands you the *installed* toolchain version. That version governs
the research: every query you pass to `lathe_research` and every URL you fetch
must target it. A documentation URL naming a different version — fetching
`ziglang.org/documentation/0.14.0/` after the probe reported Zig 0.16.0 — is a
defect, not a near-miss; fix the URL before you write. This matters most when
search comes back empty and you are falling back on URLs you recall, which is
exactly when the version in the path is likeliest to be stale.

A toolchain that changed its API between the version you remember and the
version you probed is the failure this prevents, and it is invisible in the
finished tutorial: the prose reads fine and the code does not compile. If you
cannot reach documentation for the pinned version, that is the skill's "no web
access" branch — `[!UNVERIFIED]` the API-shaped claims and say what to check.

## Writing the part

- **One file, `part-01.md`.** Never `index.md`, never multiple parts. Pass its
  full Markdown text as `lathe_publish(markdown=…)`.
- **Diagrams:** the `diagram-design` skill does not exist here, and Mermaid is
  banned by the skill. So use a **Markdown table** or **ASCII art in a code
  block** — both are first-class options in the skill's "Visual artifacts"
  section — or no diagram at all. Do not hand-roll inline SVG.
- Everything else in the skill — the hook, `## What you'll build`,
  `## Prerequisites`, domain-specific section titles (never `## Step 1: Setup`),
  the `[!PREDICT]` beat, `## Checkpoint` with the exact command, expected output
  and likely errors, `## What's next`, 3–5 `- [ ]` `## Exercises`, the numbered
  `## Sources`, the banned first sentences, faded scaffolding, the closing
  reflection — applies unchanged.

## Your final message

After `lathe_publish` succeeds, write the handoff the skill specifies ("Tutorial
saved…", `/lathe-extend`, `/lathe-verify`, Ask), then the assumptions you made.
Keep it short.

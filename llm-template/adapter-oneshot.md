## You cannot ask questions — assume, then disclose

There is no back-and-forth here. So:

- **Skip step 1** (the experience-level question). Assume **"some familiarity"** —
  a competent programmer new to *this* topic — unless the prompt says otherwise
  ("I'm new to Rust", "I've shipped three compilers").
- **Skip step 2** (the one clarifying question). Take the most common, most
  useful reading of an ambiguous topic and commit to it.
- **Do not ask for repo or version confirmation.** The pinning step still
  happens — `lathe_probe` performs the exact detection commands the skill lists
  (`git remote get-url origin`, `git branch --show-current`, `zig version`, …).
  Take what it reports as confirmed.
- State every assumption you made — experience level, the reading you picked,
  the repo and toolchain you pinned — in your **final message**, not in the
  tutorial prose.
- "Stay in session" does not apply. This process exits after one answer.

## Tool budget — this is a hard limit, plan for it

`llm` caps the run at **five model turns**, and the fifth raises an error even if
it is your final answer. So you get **three tool-calling turns and one final
message**. Spend them exactly like this:

1. **Turn 1 — `lathe_probe(topic=…, tool_hints=…, queries=[…])`.** One call. It
   returns the environment (repo, branch, installed tool versions, the active
   voice spec) *and* search hits for your queries, together. Pass 2–4 queries
   aimed at the authoritative sources you need: official docs, the spec, the
   reference implementation.
2. **Turn 2 — `lathe_research(urls=[…])`.** One call, 3–8 URLs, batched. These
   are the sources you will actually read. Pick them from turn 1's hits, and add
   any canonical documentation URL you already know (the tool fetches any URL —
   it does not need to have come from a search). If search returned nothing
   usable, this turn is where you fetch known documentation URLs directly.
3. **Turn 3 — `lathe_publish(...)`.** One call. The complete `part-01.md` and
   every store flag.
4. **Turn 4 — your final message to the user.** Plain text. No tool calls.

Never split a turn's work across two turns.

`lathe_publish` validates structure as well as the store flags: the opening
"By the end of this part" promise, `## What you'll build`, `## Prerequisites`,
`## Checkpoint`, a `[!PREDICT]` beat, `## What's next`, `## Exercises` with 3-5
checkboxes, and `## Sources`. It refuses and names what is missing. Write the
part with all of them the first time — you have exactly one repair call, and
spending it means your final message is the turn that trips the chain limit.
The tutorial is stored either way; you just lose the handoff text.

If `lathe_research` fetched nothing usable, do **not** burn a turn retrying —
follow the skill's "No web tools in this session?" branch instead: say so in
your final message, write more conservatively, `[!UNVERIFIED]`-flag the
load-bearing unknowns, and pass `no_sources_reason` to `lathe_publish`.

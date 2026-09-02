---
name: principal
description: Dense, laconic register for expert readers — a framed picture over a paragraph where structure earns one; no warm-up, no recap, no explaining the obvious.
---

# Principal

Written for a reader who has shipped production systems, reads fast, and thinks
in shapes. The tutorial respects their time: every sentence carries new
information, rationale appears only where a choice is non-obvious, structure is
shown rather than described, and nothing is celebrated.

## Stance and register

- **Assume a senior engineer.** Never explain what the reader's job title implies
  they know — what a hash map is, why tests exist, how a REPL works. No
  prerequisites recaps, no "as you may recall", no "don't worry if". Explain only
  what is specific to this topic, this tool, this design.
- **Dense, not telegraphic.** Complete sentences, but nothing wasted. Short
  fragments are fine as transitions ("Parser done. Evaluator next.") — never as
  the main register; the prose must stay readable at speed, not decoded.
- **Show shape instead of describing it.** When the subject has structure — a
  pipeline, a memory layout, a state machine, a before/after transformation —
  prefer a table or, where the tutorial's diagram rules permit one, a diagram
  (Mermaid, ASCII layout) over a structural paragraph, and let the picture carry
  the explanation. One framing sentence before, per the tutorial's diagram
  rules; the prose after never restates the picture in words — it adds what the
  picture can't show. No decorative diagrams: if one plain sentence says it
  legibly, no picture; a diagram earns its place by showing structure a sentence
  can't carry without nesting.
- **Spatial language in prose.** Prefer wording that hands the reader a picture
  to hold — "the write head chases the read head around the ring" — over
  abstract description. One image per concept, held consistently; never a pile
  of mixed metaphors.
- **Imperative for actions, impersonal for facts.** "Run", "Add", "Measure" when
  the reader acts; design decisions stated as facts about the code, not as
  memoir or as "we decided". No persona, no first-person anecdote.
- **Rationale in one clause.** When a choice needs defending, defend it in the
  same sentence and move on: "Fixed array, not a ring buffer — the indirection
  costs more than it saves at 512 frames."
- **Code stands on its own.** The sentence before a block says what to look at;
  the block carries no comments restating it.
- **Humor: dry, rare, deadpan.** Never explained, never at the reader's expense,
  never a joke where information could be.

## Avoid

- Warm-up and wind-down: "In this part we will…", section summaries,
  "Congratulations", "Let's dive in".
- Cheerleading and hype: "Great!", "powerful", "seamless", exclamation marks.
- Hedging: "you might want to consider". State it or flag it as unverified.
- Filler adverbs: simply, just, basically, actually, really.
- Sentence-length recaps of what the reader just did. Two-word pivots ("Parser
  done.") are fine; a paragraph re-walking the previous section is not.

## Calibration — before / after

> ❌ "In this section we'll set up our project. Don't worry if you've never used
> Zig before — we'll go step by step!"
>
> ✅ "`zig init`. The build graph lives in `build.zig`; touched once per new
> artifact."

> ❌ "The audio callback pulls frames from the ring buffer, which the producer
> thread fills from the oscillator bank, which is driven by the voice allocator,
> which receives note events from the MIDI parser."
>
> ✅ "Data flows one way; the only shared state is the ring buffer between the
> two threads:
>
> ```mermaid
> flowchart LR
>   MIDI[MIDI parser] --> Alloc[Voice allocator] --> Osc[Oscillator bank] --> Ring[(Ring buffer)] --> CB[Audio callback]
> ```
>
> Everything left of the buffer runs on the producer thread; the callback owns
> everything to its right."

> ❌ "You might want to consider possibly increasing the buffer size if you
> experience audio dropouts."
>
> ✅ "Underruns at 128 frames are expected on consumer hardware. Use 512."

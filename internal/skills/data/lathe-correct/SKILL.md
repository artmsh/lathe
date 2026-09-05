---
name: lathe-correct
description: Apply a reader's inline correction to one part of a stored Lathe tutorial, in session. Use when the user invokes /lathe-correct with a slug and part like "/lathe-correct digital-synth-zig part-02.md" followed by a Note and an Excerpt block (the inline corrector in `lathe serve` pastes exactly this).
tags: [skill, lathe]
---

# Lathe — Apply an Inline Correction

A reader selected a passage in `lathe serve` and said what's wrong with it. Apply the **narrowest** edit to that one part that makes the note true. Triggered by `/lathe-correct <slug> <part>` followed by:

```
Note: <what the reader says is wrong>
Excerpt:
<<<
<the text they selected>
>>>
```

Everything about voice, shape, and research discipline comes from the **`lathe`** skill; this skill only covers locating the excerpt, judging the note, and the write → `correct-commit` handshake.

## Steps

1. **Locate the excerpt** in `~/.lathe/tutorials/<slug>/<part>`.
   - The reader selected from *rendered HTML*, not from the markdown source: smart quotes, stripped emphasis markers, rendered fences and collapsed whitespace all mean the excerpt is rarely a byte-exact substring. Grep for the most distinctive run of words in it, then confirm by surrounding context.
   - **There are no offsets to trust.** The browser sends text, not positions. Never compute a character range from the excerpt's length.
   - If you cannot locate it confidently, or it matches in several places and context doesn't disambiguate: **stop, change nothing, and say so.** A wrong-location edit is far worse than an unapplied correction.

2. **Judge the note before obeying it.** The reader is often right and sometimes not. If the note is a factual claim, ground it the way the `lathe` skill grounds any load-bearing claim — check the authoritative source, not your memory. If the tutorial is right and the note is wrong, **do not edit**; say so and explain why. If the note is a matter of taste that fights the tutorial's voice, prefer the voice.

3. **Apply the narrowest edit** that satisfies the note.
   - **One file: this part only.** Don't touch sibling parts, don't write `index.md`, don't edit `metadata.json`, don't restructure sections, don't re-voice surrounding prose.
   - Writing the part markdown directly into the tutorial dir is the one allowed content write — the same sanctioned write `/lathe-extend` step 4 has. The binary owns *metadata*; the skill writes *part bodies*.
   - If the fix invalidates a later part (a renamed symbol used downstream, say), **don't** chase it into that part. Apply this one, then flag the knock-on in your report so the reader can send a second correction.

4. **Onboarding guides (`kind: onboarding` in `metadata.json`) — never rewrite an anchored fence body.** The content inside a ```` ```go path=… lines=… ```` fence is derived from the pinned repository, and the drift machinery owns it; editing it by hand fabricates repo content. Prose around the fence is fair game. If the correction is genuinely about the fenced code, say that the fix belongs in the repository (or in a re-pin via `/lathe-verify`), and leave the fence alone.

5. **Record it:**

   ```bash
   lathe correct-commit <slug> <part>
   ```

   This resets the tutorial to `unverified` — the edited part is no longer covered by whatever verification preceded it. It refuses while the tutorial is verifying or extending; if it does, don't force it, just report that.

6. **Onboarding guides only:** after committing, run `lathe drift <slug>` and report the result, so a prose edit that broke an anchor surfaces immediately.

7. **Report** in one line: what you changed, or why you didn't. When `/lathe-work` dispatched you, that line is what the reader sees in their browser — "left unchanged: …" is a normal, expected outcome, and saying so beats silence.

## Boundaries

- The **only durable-state write** is `lathe correct-commit`. Never edit `metadata.json` directly.
- Rewriting the one named part body is the sole content write, and it's required.
- Don't verify, don't extend, don't re-tag, don't reformat the file wholesale.
- Not confident where the excerpt lives? Stop. Report. Change nothing.

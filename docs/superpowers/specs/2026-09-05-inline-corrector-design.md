# Inline corrector — design

Date: 2026-09-05
Status: approved, ready for an implementation plan

## Problem

A reader spotting a wrong sentence, a stale flag, or a typo in a served
tutorial has no way to say so from the page. The only paths are the Ask
drawer (which answers a question but changes nothing) and editing
`~/.lathe/tutorials/<slug>/part-NN.md` by hand.

## What we're building

Select text inside the article on a reading page → a small popup appears
anchored to the selection, carrying a one-line text field and an **Apply**
button. Submitting sends the selected excerpt plus the reader's note to a
connected `/lathe-work` worker, which applies the narrowest edit that
satisfies the note, rewrites that one part file, and records the change
through the CLI. With no worker connected, the popup hands back a
paste-able command instead — the same fallback Ask/Verify/Extend already
have.

The boundary is unchanged: the Go binary never drives a model. The queue
is the bridge; the model work happens in the user's interactive session.

## Architecture

Every piece mirrors an existing one. The correction path is the ask path
with a write at the end.

```
browser (selection popup)
  └─ POST /-/correct/{slug}/{part}          internal/serve/correct.go
       ├─ worker connected → queue.Enqueue(JobCorrect)   → writeQueued
       └─ no worker        → writeHandoff("/lathe-correct …")
              ↓
lathe work next  →  /lathe-correct skill (interactive session)
       ├─ locate excerpt in part markdown
       ├─ rewrite part-NN.md   (the sanctioned content write)
       ├─ lathe correct-commit <slug> <part>   → Status = unverified
       └─ lathe work done <id>
              ↓
browser polls GET /-/work/{id} → on done, offers a reload
```

### 1. Queue contract

`internal/queue/queue.go`:

```go
JobCorrect JobType = "correct"
```

`Job` gains two fields, both `omitempty`:

```go
Excerpt string `json:"excerpt,omitempty"`  // the text the reader selected
Note    string `json:"note,omitempty"`     // what they want changed
```

`Question` and `Guidance` are deliberately **not** overloaded — the worker
dispatches on job shape, and a correction carries two distinct payloads
where ask carries one.

### 2. HTTP endpoint

New file `internal/serve/correct.go`, route
`POST /-/correct/{slug}/{part}` registered beside the ask route in
`server.go`.

Validation chain, copied from `ask.go` in order:

1. `sameOrigin(r)` → 403
2. part must end in `.md` → 404
3. `safeTutorialPath(slug)` → 404
4. `store.ReadMetadata` → 404
5. `isKnownPart(tut, part)` → 404
6. `safeTutorialPath(slug, part)` + `os.Stat` → 404
7. `readJSONBody(w, r, maxCorrectionBytes, &payload)`

Step 5 is stricter than `ask.go`, which stops at `os.Stat`. Ask is
read-only; a correction rewrites the file, so the part must be one the
metadata declares (`isKnownPart` in `server.go`, which also covers the
legacy `index.md` case) — a stray `.md` sitting in the tutorial dir is not
a correctable part.

Then, from `extend.go`, the in-flight guard:

- `tut.Status == StatusExtending || tut.Status == StatusVerifying` → 409
  `"conflict: tutorial is already extending or verifying"`

Then the standard branch:

- `s.queue.WorkerConnected()` → `Enqueue(Job{Type: JobCorrect, Slug, Part,
  Excerpt, Note})` → `writeQueued(w, id)`
- else → `writeHandoff(w, command)` with the block below.

Payload and limits:

```go
const maxCorrectionBytes = 8 << 10 // 8 KiB, matching maxQuestionBytes
```

Excerpt is capped at 4 KiB and note at 2 KiB after trimming; the client
truncates before sending, the server rejects an over-cap field with 400.
An empty note is a 400 (`"note is required"`); an empty excerpt is a 400
(`"excerpt is required"`) — a correction with no anchor has nothing to
locate.

### 3. Handoff format

A question is one line, so `/lathe-ask`'s handoff is one pasteable block.
An excerpt is not — it may be multi-line and may itself contain backticks,
so fences are unusable. The handoff uses sentinels:

```
/lathe-correct <slug> <part>
Note: <the reader's note, newlines collapsed to spaces>
Excerpt:
<<<
<the selected text, verbatim>
>>>
```

The skill treats everything between `<<<` and `>>>` on their own lines as
the excerpt.

### 4. `/lathe-correct` skill

New `.claude/skills/lathe-correct/SKILL.md`, invoked as
`/lathe-correct <slug> <part>` followed by the note/excerpt block.

Protocol:

1. **Locate.** Read `~/.lathe/tutorials/<slug>/<part>` and find the
   excerpt in the markdown source. The match is fuzzy by design: the
   rendered DOM the reader selected from is not the source (smart quotes,
   stripped markup, rendered fences), so grep for a distinctive substring
   and confirm by surrounding context. **No offset mapping** — the browser
   sends text, not positions. If the excerpt cannot be located confidently,
   stop and say so rather than guessing at a location.
2. **Judge.** The reader's note is a claim, not an instruction to obey
   blindly. Where it is a factual correction, verify it the same way the
   `lathe` skill grounds any load-bearing claim before writing it in. Where
   the note is wrong, do not edit — report that back instead.
3. **Apply narrowly.** The smallest edit that satisfies the note. One part
   file, this part only. Don't restructure, don't re-voice, don't touch
   sibling parts, don't touch `index.md`, don't edit `metadata.json`.
4. **Onboarding guides** (`kind: onboarding`): never rewrite the body of an
   anchored fence (```` ```go path=… lines=… ````) — that content is derived
   from a pinned repo file and belongs to the drift machinery. Prose around
   it is fair game. After committing, run `lathe drift <slug>` and report
   the result.
5. **Record.** `lathe correct-commit <slug> <part>`.

Writing the part body directly is the sanctioned content write, exactly as
in `/lathe-extend` step 4: the binary owns metadata, the skill writes part
bodies.

### 5. `lathe correct-commit`

New `cmd/correct-commit.go`, closest precedent `cmd/verify-result.go`:

```
lathe correct-commit <slug> <part-file>
```

- `validateSlug` on both args (the existing helper in `verify-result.go`).
- Read metadata; the part must be declared in `Parts` (or be the legacy
  `index.md` of a partless tutorial) — the same rule `isKnownPart` applies
  in the server, restated here because `cmd` cannot import `internal/serve`.
- `os.Stat` the part file under the tutorial dir; missing → error.
- Refuse when `Status` is `verifying` or `extending`
  (the same guard the endpoint applies, re-applied at the write, because a
  handoff paste bypasses the endpoint entirely).
- Set `Status = store.StatusUnverified` and write metadata. An edited part
  is no longer covered by whatever verification preceded it.

**No `StatusCorrecting` enum value.** There is nothing to reserve — unlike
`extend-start`, which reserves a filename — and a new status would ripple
into `progress.go`, `card_progress.go`, the status header partial and the
list-page filters for no gain. One command, not a start/commit pair.

### 6. Worker dispatch

`.claude/skills/lathe-work/SKILL.md` gains a `correct` branch: apply the
`/lathe-correct` protocol against `slug` / `part` / `excerpt` / `note`,
then report the outcome with `lathe work answer <id> --answer -` — **not**
`lathe work done`.

`work done` carries no message, so a browser watching a `done` job can only
say "Part updated". But the skill declines to edit in several ordinary
cases: the excerpt can't be located, it matches ambiguously, the note is
factually wrong, or the target is an anchored fence in an onboarding guide.
In every one of those the reader would be told the part changed when it
did not. `handleWorkAnswer` already calls `SetAnswer` then `Done` and never
inspects the job type, so the same endpoint serves a correction report with
no server change. The report is one line — "rewrote the paragraph on X" or
"left unchanged: couldn't find that passage in the source".

`handleWorkGet` needs no change either: the plain `answer` field is enough,
and `answerHTML` stays gated to `JobAsk`.

It also gains a **catch-all**: an unrecognised `type` → say so in chat and
`lathe work done <id>`. Stale skill installs are likely during rollout, and
without this a `correct` job sent to an old worker sits claimed until the
10-minute reclaim timeout, with the browser polling a job nobody will ever
finish.

### 7. Browser UI

In `layout.html`, a new self-contained script block:

- **Arming.** A `selectionchange` (plus `mouseup`/`keyup` for reliability)
  handler that acts only when the selection is non-empty **and**
  `article.contains(sel.anchorNode) && article.contains(sel.focusNode)` —
  so selections in the Ask drawer, the TOC rail, the nav, and the handoff
  `<pre>`s never arm it.
- **Popup.** Positioned from `range.getBoundingClientRect()`, clamped to
  the viewport, flipping above the selection when there's no room below.
  Contents: a one-line input (`placeholder="What's wrong here?"`) and an
  **Apply** button. Dismissed on Escape, outside click, or the selection
  collapsing. Styled with existing design-system tokens; markup follows the
  floating-action / drawer patterns already in the page.
- **Submit.** `POST /-/correct/{slug}/{part}` with `{excerpt, note}`.
  - `mode === "queued"` → collapse the popup to a small "Applying…" pill
    and poll `GET /-/work/{id}` every 1.5s, capped like `pollAskAnswer`,
    pausing while `document.hidden`. A `pending` flag is set on submit and
    cleared when the poll resolves; while it is set, outside-click and
    scroll do **not** dismiss the popup. Otherwise a scroll — which mobile
    selection routinely triggers — silently discards the result of a job
    that is still running.
  - `mode === "handoff"` → render the paste-able block with a Copy button,
    reusing the same `copyText` helper the code-copy pass exposes.
- **Staleness.** When the poll reports `done`, the pill shows the worker's
  `answer` via `textContent` — the one-line report from §6 — followed by a
  **Reload** button. Unlike ask, a correction rewrites the part under the
  reader, and the existing status poller never swaps article body HTML, so
  the reload is the honest affordance. A reader who is told "left
  unchanged: couldn't find that passage" simply doesn't click it. If
  `answer` is empty (an old worker that called `work done`), the pill falls
  back to "Part updated".

Every reader-typed string goes in via `textContent`, never `innerHTML` —
matching how the ask drawer treats question text.

## Testing

- `internal/serve/correct_test.go`, mirroring `ask_test.go`: method guard,
  cross-origin rejection, non-`.md` part, unknown slug, unknown part, empty
  note, empty excerpt, over-cap body, the 409 in-flight guard, and both
  branches of the worker-connected split (queued job fields populated;
  handoff string shape including the sentinels).
- `internal/queue/queue_test.go`: a `JobCorrect` round-trip carrying
  `Excerpt`/`Note` through enqueue → claim → done.
- `internal/serve/work_test.go`: `POST /-/work/{id}/answer` on a `correct`
  job records the report and closes it, and `GET /-/work/{id}` hands the
  `answer` back without an `answerHTML` key.
- `cmd/correct-commit_test.go`: status reset from `verified` and from
  `failed`; refusal while `verifying`/`extending`; missing part file;
  invalid slug.
- `internal/skills/skills_test.go` parity: `mage skills` regenerated so
  `internal/skills/data` carries `lathe-correct` and the updated
  `lathe-work`. The parity gate reds CI otherwise.
- `mage check` green before the PR.

## Docs

- `AGENTS.md`: the layout tree gains `correct.go`, `cmd/correct-commit.go`,
  `.claude/skills/lathe-correct/`, and the `correct` job type in the queue
  description.
- `README.md`: a line on the inline corrector in the `lathe serve` section.

## Out of scope

- Corrections spanning more than one part.
- Edit history, diff preview, or undo. The tutorial dir is not versioned by
  Lathe; a reader who wants a safety net keeps `~/.lathe` in git.
- Rewriting anchored fence bodies in onboarding guides.
- Any correction path that runs without an interactive session.

# Inline Corrector Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a reader select text on a `lathe serve` reading page, type what's wrong in a popup, hit Apply, and have a connected `/lathe-work` agent rewrite that part.

**Architecture:** The correction path mirrors the existing ask path end to end — a `POST /-/correct/{slug}/{part}` endpoint enqueues a new `correct` job when a worker is connected and otherwise hands back a paste-able command; a new `/lathe-correct` skill locates the excerpt in the part markdown, applies the narrowest edit, rewrites the one part file, and records it with a new `lathe correct-commit` that resets the tutorial to `unverified`. The Go binary still never drives a model.

**Tech Stack:** Go 1.x (net/http `ServeMux` with method+wildcard patterns, cobra, `embed`), mage for the build/CI gate, vanilla ES5-style browser JS in `internal/serve/layout.html`, plain `go test`.

**Spec:** `docs/superpowers/specs/2026-09-05-inline-corrector-design.md`

## Global Constraints

- **The binary never drives a model.** No task may spawn `claude`, an agent subprocess, or any headless model call from Go. Model work happens only in the user's interactive session, reached through the queue.
- **The CLI owns durable metadata; skills write part bodies only.** `metadata.json` is written exclusively by Go (`store.WriteMetadata`). The `/lathe-correct` skill's one content write is the part markdown at `~/.lathe/tutorials/<slug>/<part>` — the same sanctioned write `/lathe-extend` step 4 has.
- **No `StatusCorrecting` enum value.** The status set stays exactly: `unverified`, `verifying`, `verified`, `failed`, `skipped`, `extending`.
- Body caps, exact values: `maxCorrectionBytes = 8 << 10` (whole request), excerpt ≤ 4096 bytes after trim, note ≤ 2048 bytes after trim.
- Handoff sentinels are exactly `<<<` and `>>>`, each alone on its own line.
- Route path is exactly `POST /-/correct/{slug}/{part}`; skill command is exactly `/lathe-correct <slug> <part>`; CLI is exactly `lathe correct-commit <slug> <part-file>`.
- Every reader-supplied string rendered in the browser goes in via `textContent`, never `innerHTML`.
- `mage check` (gofmt, `go vet`, golangci-lint, `go test -race ./...`, `go build`, skills parity) must be green before the branch is done. `mage skills` must be re-run after any `.claude/skills/**` edit or the parity gate reds CI.
- Go comments in this repo explain *why*, not *what*. Match the density and tone of `internal/serve/ask.go` and `internal/queue/queue.go`.

---

### Task 1: Queue carries a `correct` job

**Files:**
- Modify: `internal/queue/queue.go` (JobType consts ~line 26, `Job` struct ~line 44)
- Test: `internal/queue/queue_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces: `queue.JobCorrect` (a `queue.JobType` whose string value is `"correct"`); `queue.Job` fields `Excerpt string` (JSON `excerpt,omitempty`) and `Note string` (JSON `note,omitempty`). Tasks 2 and 4 depend on these exact names and JSON tags.

- [ ] **Step 1: Write the failing test**

Append to `internal/queue/queue_test.go`:

```go
// A correct job carries two payloads, not one: the excerpt the reader selected
// and the note saying what's wrong. Both must survive enqueue → claim intact,
// because the worker needs them to locate and apply the edit.
func TestCorrectJobRoundTrip(t *testing.T) {
	q := New()
	id := q.Enqueue(Job{
		Type:    JobCorrect,
		Slug:    "digital-synth-zig",
		Part:    "part-02.md",
		Excerpt: "the ring buffer is 512 samples",
		Note:    "it's 1024, see the code above",
	})

	job, ok := q.Claim(context.Background())
	if !ok {
		t.Fatal("Claim returned no job")
	}
	if job.ID != id {
		t.Errorf("ID = %q, want %q", job.ID, id)
	}
	if job.Type != JobCorrect {
		t.Errorf("Type = %q, want %q", job.Type, JobCorrect)
	}
	if job.Excerpt != "the ring buffer is 512 samples" {
		t.Errorf("Excerpt = %q, want the selected text", job.Excerpt)
	}
	if job.Note != "it's 1024, see the code above" {
		t.Errorf("Note = %q, want the reader's note", job.Note)
	}
	if job.State != StateClaimed {
		t.Errorf("State = %q, want %q", job.State, StateClaimed)
	}
}

// The worker parses `lathe work next` output as JSON, so the wire names are part
// of the contract with every installed copy of the /lathe-work skill.
func TestCorrectJobJSONFieldNames(t *testing.T) {
	raw, err := json.Marshal(Job{Type: JobCorrect, Excerpt: "x", Note: "y"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"correct"`, `"excerpt":"x"`, `"note":"y"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("marshalled job = %s, want it to contain %s", raw, want)
		}
	}
	// omitempty: an ask/verify/extend job must not grow empty correction fields.
	raw, err = json.Marshal(Job{Type: JobAsk})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "excerpt") || strings.Contains(string(raw), "note") {
		t.Errorf("ask job = %s, want no excerpt/note keys", raw)
	}
}
```

Add `encoding/json` and `strings` to that file's imports if they aren't already there (`context` and `testing` already are).

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/queue/ -run 'TestCorrectJob' -v`
Expected: FAIL — `undefined: JobCorrect`, `unknown field Excerpt in struct literal`.

- [ ] **Step 3: Write the minimal implementation**

In `internal/queue/queue.go`, add to the `JobType` const block:

```go
	JobCorrect JobType = "correct"
```

and to the `Job` struct, after `Guidance`:

```go
	// Excerpt and Note belong to a correct job: the text the reader selected in
	// the browser and what they say is wrong with it. They stay separate from
	// Question/Guidance because a correction carries two payloads where ask and
	// extend carry one, and the worker dispatches on that shape.
	Excerpt string `json:"excerpt,omitempty"`
	Note    string `json:"note,omitempty"`
```

Update the `Job` doc comment's field rundown to mention correct: `…ask adds Part, Question, and (on completion) Answer; correct adds Part, Excerpt, and Note.` Update the package doc's "(ask, verify, or extend)" to "(ask, verify, extend, or correct)".

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/queue/ -v`
Expected: PASS (all tests in the package).

- [ ] **Step 5: Commit**

```bash
git add internal/queue/queue.go internal/queue/queue_test.go
git commit -m "feat(queue): add the correct job type"
```

---

### Task 2: `POST /-/correct/{slug}/{part}` endpoint

**Files:**
- Create: `internal/serve/correct.go`
- Create: `internal/serve/correct_test.go` (package `serve` — it reaches unexported handlers, like `ask_test.go`)
- Modify: `internal/serve/server.go` (route table, after the ask route at line 84)
- Modify: `internal/serve/work_test.go` (package `serve_test` — the worker-connected branch, which needs that file's `markWorkerConnected`/`claimNext` helpers)

**Interfaces:**
- Consumes: `queue.JobCorrect`, `queue.Job{Excerpt, Note}` (Task 1); existing `sameOrigin`, `s.safeTutorialPath`, `readJSONBody`, `writeQueued`, `writeHandoff`, `s.queue.WorkerConnected`.
- Produces: `func (s *Server) handleCorrect(w http.ResponseWriter, r *http.Request)`; `func correctionHandoff(slug, part, excerpt, note string) string`; `const maxCorrectionBytes = 8 << 10`; `const maxExcerptBytes = 4096`; `const maxNoteBytes = 2048`. Task 5 depends on the route path and on the JSON response shapes (`{"mode":"queued","jobId":…}` / `{"mode":"handoff","command":…}`, both already produced by the shared writers).

- [ ] **Step 1: Write the failing tests**

Create `internal/serve/correct_test.go`:

```go
package serve

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/store"
)

func postCorrect(t *testing.T, srv *Server, slug, part string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/-/correct/"+slug+"/"+part, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestCorrectHandlerValidation(t *testing.T) {
	dir := t.TempDir()
	makeTutFixture(t, dir, "tut", false)
	srv := NewServer(dir)
	ok := []byte(`{"excerpt":"512 samples","note":"it is 1024"}`)

	t.Run("unknown slug returns 404", func(t *testing.T) {
		if w := postCorrect(t, srv, "nope", "index.md", ok); w.Code != http.StatusNotFound {
			t.Errorf("unknown slug = %d, want 404", w.Code)
		}
	})
	t.Run("known slug, unknown part returns 404", func(t *testing.T) {
		if w := postCorrect(t, srv, "tut", "missing.md", ok); w.Code != http.StatusNotFound {
			t.Errorf("unknown part = %d, want 404", w.Code)
		}
	})
	t.Run("non-md part returns 404", func(t *testing.T) {
		if w := postCorrect(t, srv, "tut", "index.txt", ok); w.Code != http.StatusNotFound {
			t.Errorf("non-md part = %d, want 404", w.Code)
		}
	})
	t.Run("empty body returns 400", func(t *testing.T) {
		if w := postCorrect(t, srv, "tut", "index.md", []byte(``)); w.Code != http.StatusBadRequest {
			t.Errorf("empty body = %d, want 400", w.Code)
		}
	})
	t.Run("bad json returns 400", func(t *testing.T) {
		if w := postCorrect(t, srv, "tut", "index.md", []byte(`{not json`)); w.Code != http.StatusBadRequest {
			t.Errorf("bad json = %d, want 400", w.Code)
		}
	})
	t.Run("blank note returns 400", func(t *testing.T) {
		body := []byte(`{"excerpt":"512 samples","note":"   "}`)
		if w := postCorrect(t, srv, "tut", "index.md", body); w.Code != http.StatusBadRequest {
			t.Errorf("blank note = %d, want 400", w.Code)
		}
	})
	t.Run("blank excerpt returns 400", func(t *testing.T) {
		// A correction with no anchor has nothing to locate in the markdown.
		body := []byte(`{"excerpt":"  ","note":"it is 1024"}`)
		if w := postCorrect(t, srv, "tut", "index.md", body); w.Code != http.StatusBadRequest {
			t.Errorf("blank excerpt = %d, want 400", w.Code)
		}
	})
	t.Run("over-cap excerpt returns 400", func(t *testing.T) {
		// Under the 8 KiB whole-body cap but over the 4 KiB excerpt cap, so this
		// is a field-level rejection, not a 413.
		body := []byte(`{"excerpt":"` + strings.Repeat("a", 5000) + `","note":"fix"}`)
		if w := postCorrect(t, srv, "tut", "index.md", body); w.Code != http.StatusBadRequest {
			t.Errorf("over-cap excerpt = %d, want 400", w.Code)
		}
	})
	t.Run("oversize body returns 413", func(t *testing.T) {
		body := []byte(`{"excerpt":"` + strings.Repeat("a", 10*1024) + `","note":"fix"}`)
		if w := postCorrect(t, srv, "tut", "index.md", body); w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("oversize body = %d, want 413", w.Code)
		}
	})
	t.Run("GET on correct route is rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/-/correct/tut/index.md", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			t.Errorf("GET /-/correct = %d, want non-200", w.Code)
		}
	})
	t.Run("cross-origin post is rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/-/correct/tut/index.md", bytes.NewReader(ok))
		req.Header.Set("Origin", "http://evil.example")
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("cross-origin = %d, want 403", w.Code)
		}
	})
}

// An in-flight verify or extend owns the tutorial; a correction rewriting a part
// underneath it would corrupt what that run is checking or continuing.
func TestCorrectRejectedWhileInFlight(t *testing.T) {
	for _, status := range []store.Status{store.StatusVerifying, store.StatusExtending} {
		t.Run(string(status), func(t *testing.T) {
			dir := t.TempDir()
			tutDir := makeTutFixture(t, dir, "tut", false)
			tut, err := store.ReadMetadata(tutDir)
			if err != nil {
				t.Fatal(err)
			}
			tut.Status = status
			if err := store.WriteMetadata(tutDir, tut); err != nil {
				t.Fatal(err)
			}
			srv := NewServer(dir)
			body := []byte(`{"excerpt":"512 samples","note":"it is 1024"}`)
			if w := postCorrect(t, srv, "tut", "index.md", body); w.Code != http.StatusConflict {
				t.Errorf("correct while %s = %d, want 409", status, w.Code)
			}
		})
	}
}

// With no worker connected the reader gets a paste-able block. The excerpt is
// multi-line and may contain backticks, so it travels between sentinels rather
// than in a fence.
func TestCorrectReturnsHandoffCommand(t *testing.T) {
	dir := t.TempDir()
	makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)

	body := []byte(`{"excerpt":"line one\nline ` + "`two`" + `","note":"say 1024, not 512"}`)
	w := postCorrect(t, srv, "series", "part-02.md", body)
	if w.Code != http.StatusOK {
		t.Fatalf("valid correction = %d, want 200 (body=%q)", w.Code, w.Body.String())
	}
	got := w.Body.String()
	for _, want := range []string{
		"/lathe-correct series part-02.md",
		"Note: say 1024, not 512",
		"Excerpt:",
		"\\u003c\\u003c\\u003c", // <<< as encoding/json escapes it
		"line one",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("handoff body = %q, want it to contain %q", got, want)
		}
	}
}

// The note is collapsed to one line so the command block stays scannable; the
// excerpt keeps its line breaks because the skill matches on them.
func TestCorrectionHandoffShape(t *testing.T) {
	got := correctionHandoff("tut", "part-01.md", "alpha\nbeta", "one\ntwo")
	want := "/lathe-correct tut part-01.md\nNote: one two\nExcerpt:\n<<<\nalpha\nbeta\n>>>"
	if got != want {
		t.Errorf("correctionHandoff() =\n%q\nwant\n%q", got, want)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/serve/ -run 'Correct' -v`
Expected: FAIL — `undefined: correctionHandoff`, and the HTTP subtests 404 on an unregistered route.

- [ ] **Step 3: Write the implementation**

Create `internal/serve/correct.go`:

```go
package serve

import (
	"net/http"
	"os"
	"strings"

	"github.com/devenjarvis/lathe/internal/queue"
	"github.com/devenjarvis/lathe/internal/store"
)

// Body caps. maxCorrectionBytes bounds the whole request the way
// maxQuestionBytes does for ask; the field caps below keep one huge selection
// from crowding out the note (and keep the handoff block paste-able).
const (
	maxCorrectionBytes = 8 << 10 // 8 KiB
	maxExcerptBytes    = 4096
	maxNoteBytes       = 2048
)

// handleCorrect takes a reader's inline correction — the text they selected plus
// what they say is wrong with it — and routes it the same way ask does: enqueue a
// job when a /lathe-work worker is connected, else hand back the paste-able
// command. The binary never edits a part itself; the /lathe-correct skill does
// that in the user's interactive session and records it via `lathe
// correct-commit`.
func (s *Server) handleCorrect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	slug := r.PathValue("slug")
	part := r.PathValue("part")

	// Defense in depth: only .md files are valid parts.
	if !strings.HasSuffix(part, ".md") {
		http.NotFound(w, r)
		return
	}

	tutDir, ok := s.safeTutorialPath(slug)
	if !ok {
		http.NotFound(w, r)
		return
	}
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	partPath, ok := s.safeTutorialPath(slug, part)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if _, err := os.Stat(partPath); err != nil {
		http.NotFound(w, r)
		return
	}

	var payload struct {
		Excerpt string `json:"excerpt"`
		Note    string `json:"note"`
	}
	if !readJSONBody(w, r, maxCorrectionBytes, &payload) {
		return
	}
	excerpt := strings.TrimSpace(payload.Excerpt)
	note := strings.Join(strings.Fields(strings.TrimSpace(payload.Note)), " ")
	if excerpt == "" {
		http.Error(w, "excerpt is required", http.StatusBadRequest)
		return
	}
	if note == "" {
		http.Error(w, "note is required", http.StatusBadRequest)
		return
	}
	if len(excerpt) > maxExcerptBytes {
		http.Error(w, "excerpt too long", http.StatusBadRequest)
		return
	}
	if len(note) > maxNoteBytes {
		http.Error(w, "note too long", http.StatusBadRequest)
		return
	}

	// An in-flight verify or extend owns the tutorial: rewriting a part under it
	// would corrupt what that run is checking or continuing. Same guard as
	// handleExtend/handleVerify, and `lathe correct-commit` re-applies it for the
	// paste-the-handoff path that never touches this endpoint.
	if tut.Status == store.StatusExtending || tut.Status == store.StatusVerifying {
		http.Error(w, "conflict: tutorial is already extending or verifying", http.StatusConflict)
		return
	}

	if s.queue.WorkerConnected() {
		id := s.queue.Enqueue(queue.Job{Type: queue.JobCorrect, Slug: slug, Part: part, Excerpt: excerpt, Note: note})
		writeQueued(w, id)
		return
	}
	writeHandoff(w, correctionHandoff(slug, part, excerpt, note))
}

// correctionHandoff builds the paste-able block. Unlike ask's one-line question,
// an excerpt is multi-line and may itself contain backticks, so a fence can't
// delimit it — the <<< / >>> sentinels can, and the /lathe-correct skill reads
// them. The note is collapsed to a single line so the block stays scannable.
func correctionHandoff(slug, part, excerpt, note string) string {
	note = strings.Join(strings.Fields(note), " ")
	return "/lathe-correct " + slug + " " + part + "\n" +
		"Note: " + note + "\n" +
		"Excerpt:\n<<<\n" + excerpt + "\n>>>"
}
```

In `internal/serve/server.go`, register the route directly after the ask route:

```go
	mux.HandleFunc("POST /-/correct/{slug}/{part}", s.handleCorrect)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/serve/ -run 'Correct' -v`
Expected: PASS.

- [ ] **Step 5: Write the failing worker-connected test**

Append to `internal/serve/work_test.go` (package `serve_test`, which has the `markWorkerConnected` and `claimNext` helpers):

```go
func TestCorrectEnqueuesWhenWorkerConnected(t *testing.T) {
	dir := t.TempDir()
	// makeExtendTutorial lives in extend_test.go (same package) and writes each
	// part file to disk, which handleCorrect os.Stats.
	makeExtendTutorial(t, dir, "tut", store.StatusVerified, []string{"part-01.md"})
	srv := serve.NewServer(dir)
	markWorkerConnected(t, srv)

	body := []byte(`{"excerpt":"512 samples","note":"it is 1024"}`)
	req := httptest.NewRequest(http.MethodPost, "/-/correct/tut/part-01.md", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /-/correct = %d, want 200 (body=%q)", w.Code, w.Body.String())
	}
	var resp struct {
		Mode  string `json:"mode"`
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Mode != "queued" || resp.JobID == "" {
		t.Fatalf("response = %+v, want mode=queued with a job id", resp)
	}

	job := claimNext(t, srv)
	if job["type"] != "correct" {
		t.Errorf("job type = %v, want correct", job["type"])
	}
	if job["part"] != "part-01.md" {
		t.Errorf("job part = %v, want part-01.md", job["part"])
	}
	if job["excerpt"] != "512 samples" {
		t.Errorf("job excerpt = %v, want the selected text", job["excerpt"])
	}
	if job["note"] != "it is 1024" {
		t.Errorf("job note = %v, want the reader's note", job["note"])
	}
}
```

`makeExtendTutorial(t, dir, slug string, status store.Status, parts []string) string` is defined in `internal/serve/extend_test.go` (package `serve_test`, same package as `work_test.go`) and is what `TestVerifyEnqueuesWhenWorkerConnected` already uses. `bytes`, `encoding/json`, `net/http`, `net/http/httptest` and `store` are already imported in `work_test.go`.

- [ ] **Step 6: Run it to verify it fails, then passes**

Run: `go test ./internal/serve/ -run 'TestCorrectEnqueues' -v`
Expected: it should PASS immediately, since Step 3 already implemented the branch. If it fails, the fixture name/shape from Step 5 is wrong — fix the test, not the handler.

- [ ] **Step 7: Run the whole package and commit**

```bash
go test ./internal/serve/
git add internal/serve/correct.go internal/serve/correct_test.go internal/serve/server.go internal/serve/work_test.go
git commit -m "feat(serve): add the /-/correct endpoint"
```

---

### Task 3: `lathe correct-commit`

**Files:**
- Create: `cmd/correct-commit.go`
- Create: `cmd/correct-commit_test.go`

**Interfaces:**
- Consumes: existing `validateSlug` (defined in `cmd/verify-result.go`), `config.TutorialsDir`, `store.ReadMetadata`/`WriteMetadata`, `store.StatusUnverified`.
- Produces: the cobra command `correctCommitCmd` registered as `lathe correct-commit <slug> <part-file>`. Task 4's skill calls it by that exact name.

- [ ] **Step 1: Write the failing test**

Create `cmd/correct-commit_test.go`. Open `cmd/extend-commit_test.go` first and reuse its `writeTutorial(t, homeDir, slug, status, parts)` helper — it already exists in package `cmd`.

```go
package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devenjarvis/lathe/internal/store"
)

// An edited part is no longer covered by whatever verification preceded it, so
// committing a correction drops the tutorial back to unverified.
func TestCorrectCommitResetsStatus(t *testing.T) {
	for _, from := range []store.Status{store.StatusVerified, store.StatusFailed, store.StatusSkipped} {
		t.Run(string(from), func(t *testing.T) {
			homeDir := t.TempDir()
			t.Setenv("HOME", homeDir)
			tutDir := writeTutorial(t, homeDir, "test-slug", from, []string{"part-01.md"})
			if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte("# corrected"), 0644); err != nil {
				t.Fatal(err)
			}

			if err := correctCommitCmd.RunE(correctCommitCmd, []string{"test-slug", "part-01.md"}); err != nil {
				t.Fatalf("correct-commit: %v", err)
			}

			got, err := store.ReadMetadata(tutDir)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != store.StatusUnverified {
				t.Errorf("Status = %q, want unverified", got.Status)
			}
		})
	}
}

// The handoff path skips the HTTP endpoint entirely (the reader pastes the
// command straight into their agent), so the in-flight guard has to live here
// too — not only in handleCorrect.
func TestCorrectCommitRefusesWhileInFlight(t *testing.T) {
	for _, status := range []store.Status{store.StatusVerifying, store.StatusExtending} {
		t.Run(string(status), func(t *testing.T) {
			homeDir := t.TempDir()
			t.Setenv("HOME", homeDir)
			tutDir := writeTutorial(t, homeDir, "test-slug", status, []string{"part-01.md"})
			if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte("# x"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := correctCommitCmd.RunE(correctCommitCmd, []string{"test-slug", "part-01.md"}); err == nil {
				t.Fatalf("correct-commit while %s: want an error, got nil", status)
			}
		})
	}
}

func TestCorrectCommitRejectsBadArgs(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	writeTutorial(t, homeDir, "test-slug", store.StatusVerified, []string{"part-01.md"})

	t.Run("missing part file", func(t *testing.T) {
		if err := correctCommitCmd.RunE(correctCommitCmd, []string{"test-slug", "part-99.md"}); err == nil {
			t.Error("missing part file: want an error, got nil")
		}
	})
	t.Run("path traversal in part", func(t *testing.T) {
		if err := correctCommitCmd.RunE(correctCommitCmd, []string{"test-slug", "../escape.md"}); err == nil {
			t.Error("traversing part: want an error, got nil")
		}
	})
	t.Run("path traversal in slug", func(t *testing.T) {
		if err := correctCommitCmd.RunE(correctCommitCmd, []string{"../escape", "part-01.md"}); err == nil {
			t.Error("traversing slug: want an error, got nil")
		}
	})
}
```

Check `writeTutorial`'s signature in `cmd/extend-commit_test.go` before writing — if it writes the part files itself, drop the redundant `os.WriteFile` calls above.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/ -run 'CorrectCommit' -v`
Expected: FAIL — `undefined: correctCommitCmd`.

- [ ] **Step 3: Write the implementation**

Create `cmd/correct-commit.go`:

```go
package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

// correctCommitCmd records that a reader's inline correction has been applied to
// a part. The /lathe-correct skill rewrites the part body itself — the sole
// content write, exactly as in /lathe-extend — then calls this so the Go binary
// stays the only writer of metadata.json.
//
// There is no `correct-start` counterpart and no "correcting" status: unlike an
// extend there is nothing to reserve, and a whole new status would ripple
// through the badge, the list filters and the progress cards for no gain.
var correctCommitCmd = &cobra.Command{
	Use:   "correct-commit <slug> <part-file>",
	Short: "Record a reader correction applied to a part (used by the /lathe-correct skill)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug, partFile := args[0], args[1]
		if err := validateSlug(slug); err != nil {
			return err
		}
		if err := validateSlug(partFile); err != nil {
			return fmt.Errorf("invalid part file: %w", err)
		}
		tutorialsDir, err := config.TutorialsDir()
		if err != nil {
			return err
		}
		tutDir := filepath.Join(tutorialsDir, slug)

		if _, err := os.Stat(filepath.Join(tutDir, partFile)); err != nil {
			return fmt.Errorf("part file %q not found: %w", partFile, err)
		}

		tut, err := store.ReadMetadata(tutDir)
		if err != nil {
			return fmt.Errorf("read metadata for %q: %w", slug, err)
		}

		// The paste-the-handoff path never touches the HTTP endpoint, so the
		// in-flight guard has to be re-applied at the write.
		if tut.Status == store.StatusVerifying || tut.Status == store.StatusExtending {
			return fmt.Errorf("cannot record a correction to %q while it is %s", slug, tut.Status)
		}

		// A corrected part is no longer covered by whatever verification preceded
		// it, so the tutorial drops back to unverified.
		tut.Status = store.StatusUnverified
		if err := store.WriteMetadata(tutDir, tut); err != nil {
			return fmt.Errorf("write metadata: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Recorded a correction to %s in %q (now unverified)\n", partFile, slug)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(correctCommitCmd)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/ -run 'CorrectCommit' -v` then `go test ./cmd/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/correct-commit.go cmd/correct-commit_test.go
git commit -m "feat(cmd): add lathe correct-commit"
```

---

### Task 4: The `/lathe-correct` skill and worker dispatch

**Files:**
- Create: `.claude/skills/lathe-correct/SKILL.md`
- Modify: `.claude/skills/lathe-work/SKILL.md` (the dispatch list in "The loop" step 2, and the Boundaries section)
- Generated: `internal/skills/data/lathe-correct/SKILL.md`, `internal/skills/data/lathe-work/SKILL.md` (via `mage skills` — never hand-edited)
- Test: `internal/skills/skills_test.go`

**Interfaces:**
- Consumes: `lathe correct-commit <slug> <part-file>` (Task 3); the `correct` job shape from Task 1 (`slug`, `part`, `excerpt`, `note`).
- Produces: the `/lathe-correct` skill slug, which Task 2's handoff string and Task 6's docs both name.

- [ ] **Step 1: Write the failing test**

Append to `internal/skills/skills_test.go`:

```go
// The bundled set is the contract for `lathe skills install`; a new skill that
// never reaches data/ ships a binary that can't install it.
func TestCorrectSkillIsBundled(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	var found *Skill
	for i := range all {
		if all[i].Slug == "lathe-correct" {
			found = &all[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("lathe-correct is not bundled; got %d skills", len(all))
	}
	if found.Name == "" || found.Description == "" {
		t.Errorf("lathe-correct frontmatter = name %q, description %q; want both set", found.Name, found.Description)
	}
	if !bytes.Contains(found.Raw, []byte("lathe correct-commit")) {
		t.Error("lathe-correct SKILL.md never calls `lathe correct-commit`")
	}
}

// A worker running an older copy of lathe-work must not strand a correct job.
func TestWorkSkillDispatchesCorrect(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range all {
		if s.Slug != "lathe-work" {
			continue
		}
		for _, want := range []string{"/lathe-correct", "unrecognised", "lathe work done"} {
			if !bytes.Contains(s.Raw, []byte(want)) {
				t.Errorf("lathe-work SKILL.md missing %q", want)
			}
		}
		return
	}
	t.Fatal("lathe-work is not bundled")
}
```

Add `bytes` to the imports if it isn't already there.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/skills/ -run 'Correct' -v`
Expected: FAIL — `lathe-correct is not bundled`.

- [ ] **Step 3: Write the skill**

Create `.claude/skills/lathe-correct/SKILL.md`. Read `.claude/skills/lathe-extend/SKILL.md` first and match its register, heading style, and frontmatter shape.

```markdown
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

7. **Report** in one or two lines: what you changed, or why you didn't.

## Boundaries

- The **only durable-state write** is `lathe correct-commit`. Never edit `metadata.json` directly.
- Rewriting the one named part body is the sole content write, and it's required.
- Don't verify, don't extend, don't re-tag, don't reformat the file wholesale.
- Not confident where the excerpt lives? Stop. Report. Change nothing.
```

- [ ] **Step 4: Add the worker dispatch branch**

In `.claude/skills/lathe-work/SKILL.md`, inside "The loop" step 2, after the `ask` bullet, add:

```markdown
   - **`correct`** → apply the **`/lathe-correct`** protocol against `slug` / `part`, passing `excerpt` (the text the reader selected) and `note` (what they say is wrong). It locates the excerpt, applies the narrowest edit to that one part, and records it with `lathe correct-commit`. When it finishes, close the job:
     ```bash
     lathe work done <id>
     ```

   - **anything else** → an unrecognised `type` means this skill copy is older than the server. Don't guess at it: say so in chat and close it so the browser isn't left polling a job nobody will finish.
     ```bash
     lathe work done <id>
     ```
```

Also update the JSON example in step 1 to mention the correction fields, and add to **Boundaries**: `` `correct` → `lathe work done <id>` (the part edit is already durable via `lathe correct-commit`). ``

- [ ] **Step 5: Regenerate the embedded mirror and run the tests**

```bash
mage skills
go test ./internal/skills/ -v
```
Expected: PASS. `mage skills` must be run — `go:embed` cannot read `.claude/`, which is the entire reason `internal/skills/data` exists, and `mage skillsCheck` reds CI on drift.

- [ ] **Step 6: Commit**

```bash
git add .claude/skills/lathe-correct .claude/skills/lathe-work/SKILL.md internal/skills/data internal/skills/skills_test.go
git commit -m "feat(skills): add /lathe-correct and wire it into the worker loop"
```

---

### Task 5: The selection popup

**Files:**
- Modify: `internal/serve/layout.html` (new `<script>` block before `{{template "liveNudge" .}}`, and popup markup beside `#askBackdrop`)
- Modify: `internal/serve/styles.css` (append near the `#askDrawer` block, ~line 530)
- Test: `internal/serve/server_test.go` (the reading page carries the popup markup)

**Interfaces:**
- Consumes: `POST /-/correct/{slug}/{part}` from Task 2, returning `{"mode":"queued","jobId":"…"}` or `{"mode":"handoff","command":"…"}`; the existing `GET /-/work/{id}` poll (`{state, …}`); the `#askDrawer`'s `data-slug` / `data-part` attributes for the current slug and part.
- Produces: no JS API other pages consume; `#correctPopup` is self-contained.

- [ ] **Step 1: Write the failing test**

Append to `internal/serve/server_test.go` (find how the existing reading-page tests build a server and request a part page, and match that setup exactly):

```go
// The inline corrector ships with the reading page, so a reader can select and
// correct without any extra request.
func TestPartPageCarriesCorrectorMarkup(t *testing.T) {
	dir := t.TempDir()
	makeTestTutorial(t, dir, "test-series", true)
	srv := serve.NewServer(dir)

	req := httptest.NewRequest(http.MethodGet, "/test-series/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET part page = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`id="correctPopup"`,
		`id="correctInput"`,
		"/-/correct/",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("part page missing %q", want)
		}
	}
}
```

`makeTestTutorial(t, dir, slug string, series bool) string` is the existing helper at the top of `server_test.go` (package `serve_test`); `series: true` gives it `part-01.md` and `part-02.md`. The `/test-series/part-01.md` request shape matches the neighbouring reading-page tests.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/serve/ -run 'CorrectorMarkup' -v`
Expected: FAIL — the markup isn't there.

- [ ] **Step 3: Add the markup**

In `layout.html`, immediately after `<div id="askBackdrop" hidden></div>`:

```html
<div id="correctPopup" hidden role="dialog" aria-label="Suggest a correction">
  <form id="correctForm">
    <input type="text" id="correctInput" placeholder="What's wrong here?" autocomplete="off" maxlength="500">
    <button type="submit" id="correctApply" class="btn btn-primary btn-sm">Apply</button>
  </form>
  <div id="correctStatus" hidden></div>
</div>
```

- [ ] **Step 4: Add the styles**

Append to `styles.css` after the `#askBackdrop` rules:

```css
/* Inline corrector: a popup anchored to the reader's selection inside the
   article. Positioned from JS (fixed coords), so only the surface is styled
   here. */
#correctPopup{position:fixed;z-index:95;background:var(--surface);border:1px solid var(--border-strong);border-radius:var(--radius-md);box-shadow:var(--shadow-md);padding:.5rem;max-width:min(360px,92vw)}
#correctForm{display:flex;gap:.4rem;align-items:center}
#correctInput{flex:1;min-width:0;font-family:var(--font-body);font-size:.9rem;color:var(--text);background:var(--bg);border:1px solid var(--border);border-radius:var(--radius-sm);padding:.35rem .5rem}
#correctInput:focus{outline:2px solid var(--accent);outline-offset:1px}
#correctStatus{margin-top:.5rem;font-size:.85rem;color:var(--text-muted)}
#correctStatus .handoff-cmd{margin:.4rem 0}
#correctStatus button{margin-top:.3rem}
```

- [ ] **Step 5: Add the script**

In `layout.html`, add this block just before `{{template "liveNudge" .}}`:

```html
<script>
  // Inline corrector. Selecting text inside the article arms a popup with a
  // one-line field; Apply posts the excerpt plus the note to /-/correct, which
  // either enqueues a job for a connected /lathe-work agent or hands back the
  // paste-able command. Unlike Ask, a correction rewrites the part on disk — so
  // when the job finishes we offer a reload rather than pretending the page is
  // still current.
  (function(){
    var popup = document.getElementById('correctPopup');
    var form = document.getElementById('correctForm');
    var input = document.getElementById('correctInput');
    var apply = document.getElementById('correctApply');
    var statusEl = document.getElementById('correctStatus');
    var drawer = document.getElementById('askDrawer');
    var article = document.querySelector('article') || document.querySelector('main');
    if (!popup || !form || !article || !drawer) return;

    var slug = drawer.getAttribute('data-slug') || '';
    var part = drawer.getAttribute('data-part') || '';
    if (!slug || !part) return;

    var excerpt = '';
    var MAX_EXCERPT = 4096;

    function hide(){
      popup.hidden = true;
      statusEl.hidden = true;
      statusEl.textContent = '';
      input.value = '';
      input.disabled = false;
      apply.disabled = false;
      excerpt = '';
    }

    // Anchor the popup under the selection, clamped to the viewport and flipped
    // above when there's no room below.
    function placeAt(rect){
      popup.hidden = false;
      var w = popup.offsetWidth, h = popup.offsetHeight;
      var left = Math.max(8, Math.min(rect.left, window.innerWidth - w - 8));
      var top = rect.bottom + 8;
      if (top + h > window.innerHeight - 8) top = Math.max(8, rect.top - h - 8);
      popup.style.left = left + 'px';
      popup.style.top = top + 'px';
    }

    // Arm only for selections that live entirely inside the article: the Ask
    // drawer, the TOC rail, the nav and the handoff <pre>s must never trigger it.
    function inArticle(node){
      return node && article.contains(node.nodeType === 1 ? node : node.parentNode);
    }

    function onSelect(){
      if (!popup.hidden && popup.contains(document.activeElement)) return;
      var sel = document.getSelection();
      if (!sel || sel.isCollapsed || sel.rangeCount === 0){ hide(); return; }
      var text = sel.toString().trim();
      if (!text){ hide(); return; }
      if (!inArticle(sel.anchorNode) || !inArticle(sel.focusNode)){ hide(); return; }
      excerpt = text.length > MAX_EXCERPT ? text.slice(0, MAX_EXCERPT) : text;
      placeAt(sel.getRangeAt(0).getBoundingClientRect());
    }

    document.addEventListener('mouseup', function(){ setTimeout(onSelect, 0); });
    document.addEventListener('keyup', function(ev){
      if (ev.key === 'Shift' || ev.key.indexOf('Arrow') === 0) setTimeout(onSelect, 0);
    });
    document.addEventListener('keydown', function(ev){
      if (ev.key === 'Escape' && !popup.hidden) hide();
    });
    document.addEventListener('mousedown', function(ev){
      if (!popup.hidden && !popup.contains(ev.target)) hide();
    });
    window.addEventListener('scroll', function(){ if (!popup.hidden) hide(); }, {passive: true});

    function showStatus(text){
      statusEl.hidden = false;
      statusEl.textContent = text;
    }

    // No worker: render the paste-able block with a Copy button, reusing the
    // page's clipboard helper so it works outside a secure context too.
    function showHandoff(command){
      statusEl.hidden = false;
      statusEl.textContent = '';
      var note = document.createElement('p');
      note.className = 'handoff-note';
      note.textContent = 'Paste this into your coding agent to apply it there:';
      statusEl.appendChild(note);
      var pre = document.createElement('pre');
      pre.className = 'handoff-cmd';
      pre.textContent = command;
      statusEl.appendChild(pre);
      var copy = document.createElement('button');
      copy.type = 'button';
      copy.className = 'btn btn-sm';
      copy.textContent = 'Copy';
      copy.addEventListener('click', function(){
        var done = function(){ copy.textContent = 'Copied'; setTimeout(function(){ copy.textContent = 'Copy'; }, 1500); };
        if (window.latheCopyText) window.latheCopyText(command).then(done).catch(function(){ copy.textContent = 'Failed'; });
        else if (navigator.clipboard) navigator.clipboard.writeText(command).then(done);
      });
      statusEl.appendChild(copy);
    }

    // The part changed on disk once the job is done, so the honest affordance is
    // a reload — the status poller only swaps the badge/verify/extend regions,
    // never the article body.
    function offerReload(){
      statusEl.hidden = false;
      statusEl.textContent = 'Part updated. ';
      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'btn btn-sm';
      btn.textContent = 'Reload';
      btn.addEventListener('click', function(){ window.location.reload(); });
      statusEl.appendChild(btn);
    }

    function pollJob(jobId){
      showStatus('Applying…');
      var attempts = 0, maxAttempts = 400; // ~10 min at 1.5s
      var timer = setInterval(function(){
        if (document.hidden) return;
        attempts++;
        if (attempts > maxAttempts){
          clearInterval(timer);
          showStatus('No result yet — make sure /lathe-work is running in your agent.');
          return;
        }
        fetch('/-/work/' + encodeURIComponent(jobId), {headers: {'Accept': 'application/json'}})
          .then(function(r){ return r.ok ? r.json() : null; })
          .then(function(data){
            if (!data || data.state !== 'done') return;
            clearInterval(timer);
            offerReload();
          })
          .catch(function(){ /* transient blip — keep polling */ });
      }, 1500);
    }

    form.addEventListener('submit', function(ev){
      ev.preventDefault();
      var note = (input.value || '').trim();
      if (!note || !excerpt) return;
      input.disabled = true;
      apply.disabled = true;
      showStatus('Sending…');

      fetch('/-/correct/' + encodeURIComponent(slug) + '/' + encodeURIComponent(part), {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({excerpt: excerpt, note: note})
      }).then(function(response){
        if (!response.ok) return response.text().then(function(t){ throw new Error(t || ('HTTP ' + response.status)); });
        return response.json().then(function(data){
          if (data.mode === 'queued') pollJob(data.jobId);
          else showHandoff(data.command || '');
        });
      }).catch(function(err){
        showStatus('Error: ' + (err && err.message ? err.message : 'request failed'));
        input.disabled = false;
        apply.disabled = false;
      });
    });
  })();
</script>
```

- [ ] **Step 6: Expose the clipboard helper**

The code-copy block already defines `copyText` but only exports `latheEnhanceCodeCopy`. In that block (around line 618 of `layout.html`), add one line beside the existing export so the corrector can reuse it instead of duplicating the fallback path:

```javascript
    window.latheCopyText = copyText;
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/serve/ -run 'CorrectorMarkup' -v` then `go test ./internal/serve/`
Expected: PASS.

- [ ] **Step 8: Verify it in a browser**

```bash
go build -o lathe && ./lathe serve
```
Then, per the repo's local-UI rule, check the rendered DOM:
```bash
~/.local/bin/lightpanda fetch --with-frames http://localhost:<port>/<slug>/part-01.md | grep -c correctPopup
```
Manually: select a sentence in the article (popup appears), select text in the Ask drawer (no popup), press Escape (popup closes), submit with no worker running (handoff block with a working Copy).

- [ ] **Step 9: Commit**

```bash
git add internal/serve/layout.html internal/serve/styles.css internal/serve/server_test.go
git commit -m "feat(serve): inline corrector popup on text selection"
```

---

### Task 6: Docs and the CI gate

**Files:**
- Modify: `AGENTS.md` (the layout tree, the `internal/serve/` and `cmd/` entries, the `.claude/skills/` list, and the queue paragraph in "What this is")
- Modify: `README.md` (the `lathe serve` section)

**Interfaces:**
- Consumes: everything from Tasks 1–5.
- Produces: nothing code depends on.

- [ ] **Step 1: Update AGENTS.md**

Four edits:
1. In the layout tree under `cmd/`, after the `extend-start.go, extend-commit.go` line: `  correct-commit.go               lathe correct-commit — skill records a reader correction applied to a part (status → unverified)`
2. Under `internal/serve/`, extend the `ask.go, verify.go, extend.go` line to `ask.go, verify.go, extend.go, correct.go` and note that correct carries the excerpt/note payload and the `<<< >>>` handoff sentinels.
3. Under `.claude/skills/`: `  lathe-correct/SKILL.md          /lathe-correct — applies a reader's inline correction to one part`
4. In "What this is", the sentence naming the buttons: add the inline corrector to the Ask/Verify/Extend list, and in the `internal/queue/` tree entry name the fourth job type.

- [ ] **Step 2: Update README.md**

In the `lathe serve` section, after the Ask description, add a short paragraph: selecting text in a part opens a popup for a one-line note; Apply sends it to a connected `/lathe-work` agent which edits that part and drops the tutorial back to `unverified`, or hands back a `/lathe-correct` command to paste when no agent is connected.

- [ ] **Step 3: Run the full gate**

```bash
mage check
```
Expected: green — gofmt, `go vet`, golangci-lint, `go test -race ./...`, `go build`, and the skills parity check. Fix anything it reports; do not commit red.

- [ ] **Step 4: Commit**

```bash
git add AGENTS.md README.md
git commit -m "docs: document the inline corrector"
```

---

## Self-Review Notes

Spec coverage check, section by section:

- Queue contract → Task 1
- HTTP endpoint (validation chain, caps, 409, queued/handoff branch) → Task 2
- Handoff sentinel format → Task 2 (`correctionHandoff` + its unit test)
- `/lathe-correct` skill (locate, judge, apply narrowly, onboarding fences, drift re-run) → Task 4
- `lathe correct-commit` (status reset, no new enum value, guard re-applied) → Task 3
- Worker dispatch + unknown-type catch-all → Task 4
- Browser UI (arming, popup, submit, staleness reload) → Task 5
- Testing section → distributed across Tasks 1–5; `mage check` in Task 6
- Docs → Task 6

Names used consistently across tasks: `JobCorrect`, `Job.Excerpt`, `Job.Note`, `handleCorrect`, `correctionHandoff`, `maxCorrectionBytes`, `maxExcerptBytes`, `maxNoteBytes`, `correctCommitCmd`, `#correctPopup`, `#correctInput`, `window.latheCopyText`.

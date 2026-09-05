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
	// Stricter than handleAsk, which stops at the os.Stat below. Ask is
	// read-only; a correction rewrites the file, so the part has to be one the
	// metadata declares — a stray .md in the tutorial dir is not correctable.
	// isKnownPart (server.go) also covers the legacy partless index.md.
	if !isKnownPart(tut, part) {
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

package serve

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	t.Run("undeclared md file in the tutorial dir returns 404", func(t *testing.T) {
		// The file exists, so os.Stat alone would admit it. Only parts the
		// metadata declares may be rewritten.
		stray := filepath.Join(dir, "tut", "notes.md")
		if err := os.WriteFile(stray, []byte("# scratch"), 0644); err != nil {
			t.Fatal(err)
		}
		if w := postCorrect(t, srv, "tut", "notes.md", ok); w.Code != http.StatusNotFound {
			t.Errorf("undeclared part = %d, want 404", w.Code)
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

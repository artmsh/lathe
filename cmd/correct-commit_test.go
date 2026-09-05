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
			// writeTutorial writes part-01.md itself; overwrite it to stand in for
			// the edit the /lathe-correct skill would have just made.
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
			writeTutorial(t, homeDir, "test-slug", status, []string{"part-01.md"})
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
	t.Run("undeclared md file in the tutorial dir", func(t *testing.T) {
		// The file exists, so an os.Stat-only check would accept it. Only parts
		// the metadata declares count — mirroring isKnownPart in the server.
		stray := filepath.Join(homeDir, ".lathe", "tutorials", "test-slug", "notes.md")
		if err := os.WriteFile(stray, []byte("# scratch"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := correctCommitCmd.RunE(correctCommitCmd, []string{"test-slug", "notes.md"}); err == nil {
			t.Error("undeclared part: want an error, got nil")
		}
	})
}

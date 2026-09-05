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

		tut, err := store.ReadMetadata(tutDir)
		if err != nil {
			return fmt.Errorf("read metadata for %q: %w", slug, err)
		}
		if !declaredPart(tut, partFile) {
			return fmt.Errorf("%q is not a part of %q", partFile, slug)
		}
		if _, err := os.Stat(filepath.Join(tutDir, partFile)); err != nil {
			return fmt.Errorf("part file %q not found: %w", partFile, err)
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

// declaredPart reports whether partFile is one of the tutorial's declared parts,
// or the legacy single-file index.md of a tutorial that was never split. It
// restates isKnownPart from internal/serve, which cmd cannot import; a
// correction rewrites a file, so "it exists on disk" is not a strong enough
// check.
func declaredPart(tut *store.Tutorial, partFile string) bool {
	if partFile == "index.md" {
		return len(tut.Parts) == 0
	}
	for _, p := range tut.Parts {
		if p == partFile {
			return true
		}
	}
	return false
}

func init() {
	rootCmd.AddCommand(correctCommitCmd)
}

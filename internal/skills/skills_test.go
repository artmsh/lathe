package skills

import (
	"bytes"
	"strings"
	"testing"
)

func TestAllReturnsEverySkillWithMetadata(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatalf("All() error: %v", err)
	}
	const want = 9
	if len(all) != want {
		t.Fatalf("All() returned %d skills, want %d", len(all), want)
	}

	wantSlugs := map[string]bool{
		"lathe":         false,
		"lathe-ask":     false,
		"lathe-correct": false,
		"lathe-extend":  false,
		"lathe-onboard": false,
		"lathe-tag":     false,
		"lathe-verify":  false,
		"lathe-voice":   false,
		"lathe-work":    false,
	}
	for _, s := range all {
		if _, ok := wantSlugs[s.Slug]; !ok {
			t.Errorf("unexpected slug %q", s.Slug)
			continue
		}
		wantSlugs[s.Slug] = true
		if s.Name == "" {
			t.Errorf("skill %q has empty name", s.Slug)
		}
		if strings.TrimSpace(s.Description) == "" {
			t.Errorf("skill %q has empty description", s.Slug)
		}
		if len(s.Raw) == 0 {
			t.Errorf("skill %q has empty raw bytes", s.Slug)
		}
	}
	for slug, found := range wantSlugs {
		if !found {
			t.Errorf("missing expected skill %q", slug)
		}
	}
}

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
		// The correct branch reports through `work answer` (the browser shows the
		// one-liner); the unknown-type catch-all closes with `work done`.
		for _, want := range []string{"/lathe-correct", "unrecognised", "lathe work answer", "lathe work done"} {
			if !bytes.Contains(s.Raw, []byte(want)) {
				t.Errorf("lathe-work SKILL.md missing %q", want)
			}
		}
		return
	}
	t.Fatal("lathe-work is not bundled")
}

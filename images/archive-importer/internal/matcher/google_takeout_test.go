package matcher

import (
	"path/filepath"
	"testing"
)

const fixtureRoot = "../../testdata/fixtures/google-takeout-minimal"

func TestGoogleTakeout_DetectsMinimalFixture(t *testing.T) {
	p := GoogleTakeoutProvider()
	ok, base, err := p.Detect(fixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("expected detection")
	}
	want := filepath.Join(fixtureRoot, "Takeout")
	if base != want {
		t.Errorf("base = %q, want %q", base, want)
	}
}

func TestGoogleTakeout_RejectsNonArchive(t *testing.T) {
	p := GoogleTakeoutProvider()
	ok, _, _ := p.Detect("../../testdata/fixtures/not-an-archive")
	if ok {
		t.Error("expected no detection")
	}
}

func TestGoogleTakeout_SubtreeMatchers(t *testing.T) {
	base := filepath.Join(fixtureRoot, "Takeout")
	p := GoogleTakeoutProvider()

	expect := map[string]string{
		"Mail":                      "archive/google-takeout/mail",
		"Calendar":                  "archive/google-takeout/calendar",
		"Chat":                      "archive/google-takeout/chat",
		"Keep":                      "archive/google-takeout/keep",
		"NotebookLM":                "archive/google-takeout/notebooklm",
		"Voice":                     "archive/google-takeout/voice",
		"My Activity":               "archive/google-takeout/my-activity",
		"Google Photos":             "archive/google-takeout/photos",
		"Location History":          "archive/google-takeout/timeline",
		"YouTube and YouTube Music": "archive/google-takeout/youtube",
		"Fit":                       "archive/google-takeout/fit",
		"Drive":                     "archive/google-takeout/drive",
		"Tasks":                     "archive/google-takeout/tasks",
		"Contacts":                  "archive/google-takeout/contacts",
	}

	for dirName, mediaType := range expect {
		t.Run(dirName, func(t *testing.T) {
			matched := false
			for _, m := range p.Subtrees {
				ok, err := m.Matches(filepath.Join(base, dirName), dirName)
				if err != nil {
					t.Errorf("matcher %q on %q: %v", m.MediaType(), dirName, err)
					continue
				}
				if ok {
					if m.MediaType() != mediaType {
						t.Errorf("dir %q matched %q, expected %q",
							dirName, m.MediaType(), mediaType)
					}
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("no matcher claimed %q", dirName)
			}
		})
	}
}

func TestGoogleTakeout_UnknownSubtreeNotMatched(t *testing.T) {
	p := GoogleTakeoutProvider()
	dirName := "SomeUnknownService"
	dirPath := "../../testdata/fixtures/google-takeout-with-unknown/Takeout/SomeUnknownService"
	for _, m := range p.Subtrees {
		ok, _ := m.Matches(dirPath, dirName)
		if ok {
			t.Errorf("matcher %q falsely claimed %q", m.MediaType(), dirName)
		}
	}
}

package matcher

import (
	"os"
	"path/filepath"
	"testing"
)

const metaFixtureRoot = "../../testdata/fixtures/meta-facebook-minimal"

func TestMeta_DetectsMinimalFixture(t *testing.T) {
	p := MetaProvider()
	ok, base, err := p.Detect(metaFixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("expected detection")
	}
	if base != metaFixtureRoot {
		t.Errorf("base = %q, want %q (Meta has no wrapper dir)", base, metaFixtureRoot)
	}
}

func TestMeta_RejectsNonArchive(t *testing.T) {
	p := MetaProvider()
	ok, _, _ := p.Detect("../../testdata/fixtures/not-an-archive")
	if ok {
		t.Error("expected no detection")
	}
}

func TestMeta_RejectsGoogleTakeout(t *testing.T) {
	p := MetaProvider()
	ok, _, _ := p.Detect("../../testdata/fixtures/google-takeout-minimal")
	if ok {
		t.Error("MetaProvider should not match Google Takeout fixture")
	}
}

func TestMeta_RejectsArbitraryJSON(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "personal_information", "profile_information")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// Looks like JSON but lacks the profile_v2 / profile_information_v2 key.
	if err := os.WriteFile(filepath.Join(dir, "profile_information.json"),
		[]byte(`{"unrelated":"shape"}`), 0644); err != nil {
		t.Fatal(err)
	}
	ok, _, err := MetaProvider().Detect(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("foreign JSON at fingerprint path should not detect as Meta")
	}
}

func TestMeta_AcceptsProfileInformationV2(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "personal_information", "profile_information")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profile_information.json"),
		[]byte(`{"profile_information_v2":{"name":{"full_name":"x"}}}`), 0644); err != nil {
		t.Fatal(err)
	}
	ok, _, err := MetaProvider().Detect(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("profile_information_v2 (newer schema name) should detect as Meta")
	}
}

func TestMeta_SubtreeMatchers(t *testing.T) {
	p := MetaProvider()

	expect := map[string]string{
		"ads_information":                   "archive/meta-facebook/ads-information",
		"apps_and_websites_off_of_facebook": "archive/meta-facebook/apps-and-websites-off",
		"connections":                       "archive/meta-facebook/connections",
		"logged_information":                "archive/meta-facebook/logged-information",
		"personal_information":              "archive/meta-facebook/personal-information",
		"preferences":                       "archive/meta-facebook/preferences",
		"security_and_login_information":    "archive/meta-facebook/security-and-login",
		"your_facebook_activity":            "archive/meta-facebook/activity",
	}

	for dirName, mediaType := range expect {
		t.Run(dirName, func(t *testing.T) {
			matched := false
			for _, m := range p.Subtrees {
				ok, err := m.Matches(filepath.Join(metaFixtureRoot, dirName), dirName)
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

func TestMeta_UnknownSubtreeNotMatched(t *testing.T) {
	p := MetaProvider()
	tmp := t.TempDir()
	d := filepath.Join(tmp, "novel_meta_category")
	if err := os.MkdirAll(d, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "x.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, m := range p.Subtrees {
		ok, _ := m.Matches(d, "novel_meta_category")
		if ok {
			t.Errorf("matcher %s should not claim unknown dir", m.MediaType())
		}
	}
}

func TestMeta_UmbrellaAndUnrecognizedMediaType(t *testing.T) {
	p := MetaProvider()
	if p.UmbrellaMediaType != "archive/meta-facebook" {
		t.Errorf("UmbrellaMediaType = %q", p.UmbrellaMediaType)
	}
	if p.UnrecognizedSubtreeMediaType() != "archive/meta-facebook/unrecognized-subtree" {
		t.Errorf("UnrecognizedSubtreeMediaType = %q", p.UnrecognizedSubtreeMediaType())
	}
}

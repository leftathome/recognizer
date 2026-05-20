package matcher

import (
	"os"
	"path/filepath"
	"strings"
)

// GoogleTakeoutProvider returns the Provider that detects Google Takeout
// archives and matches their per-service subtrees.
func GoogleTakeoutProvider() Provider {
	return Provider{
		Name: "google-takeout",
		Detect: func(rootPath string) (bool, string, error) {
			tk := filepath.Join(rootPath, "Takeout")
			fi, err := os.Stat(tk)
			if err != nil {
				if os.IsNotExist(err) {
					return false, "", nil
				}
				return false, "", err
			}
			if !fi.IsDir() {
				return false, "", nil
			}
			return true, tk, nil
		},
		Subtrees: []SubtreeMatcher{
			dirMatcher{name: "Mail", mt: "archive/google-takeout/mail", desc: "Gmail export", fingerprint: anyFileMatching("*.mbox")},
			dirMatcher{name: "Calendar", mt: "archive/google-takeout/calendar", desc: "Google Calendar export", fingerprint: anyFileMatching("*.ics")},
			dirMatcher{nameAny: []string{"Chat", "Google Chat"}, mt: "archive/google-takeout/chat", desc: "Google Chat export", fingerprint: anySubdirOf("Groups", "Conversations")},
			dirMatcher{name: "Keep", mt: "archive/google-takeout/keep", desc: "Google Keep export", fingerprint: anyFileMatching("*.json")},
			dirMatcher{name: "NotebookLM", mt: "archive/google-takeout/notebooklm", desc: "NotebookLM export", fingerprint: anyFileMatching("*.html")},
			dirMatcher{name: "Voice", mt: "archive/google-takeout/voice", desc: "Google Voice export", fingerprint: anySubdirOf("Calls", "Texts", "Voicemails")},
			dirMatcher{name: "My Activity", mt: "archive/google-takeout/my-activity", desc: "Google My Activity", fingerprint: anyFileMatchingOneOf("*.html", "*.json")},
			dirMatcher{nameAny: []string{"Google Photos"}, mt: "archive/google-takeout/photos", desc: "Google Photos export", fingerprint: anyFileMatching("*.json")},
			dirMatcher{nameAny: []string{"Location History", "Timeline"}, mt: "archive/google-takeout/timeline", desc: "Google location timeline", fingerprint: anyFileMatching("*.json")},
			dirMatcher{nameAny: []string{"YouTube and YouTube Music"}, mt: "archive/google-takeout/youtube", desc: "YouTube + Music export", fingerprint: anySubdirOf("videos", "playlists")},
			dirMatcher{name: "Fit", mt: "archive/google-takeout/fit", desc: "Google Fit", fingerprint: anySubdirOf("Activity")},
			dirMatcher{name: "Drive", mt: "archive/google-takeout/drive", desc: "Google Drive", fingerprint: anyFileMatching("*.*")},
			dirMatcher{name: "Tasks", mt: "archive/google-takeout/tasks", desc: "Google Tasks", fingerprint: anyFileMatching("*.json")},
			dirMatcher{name: "Contacts", mt: "archive/google-takeout/contacts", desc: "Google Contacts", fingerprint: anyFileMatching("*.vcf")},
		},
	}
}

// dirMatcher implements SubtreeMatcher via directory-name match plus a
// fingerprint function over the directory contents.
type dirMatcher struct {
	name        string
	nameAny     []string
	mt          string
	desc        string
	fingerprint func(dirPath string) (bool, error)
}

func (d dirMatcher) MediaType() string   { return d.mt }
func (d dirMatcher) Description() string { return d.desc }
func (d dirMatcher) Matches(dirPath, dirName string) (bool, error) {
	if !d.nameMatches(dirName) {
		return false, nil
	}
	if d.fingerprint == nil {
		return true, nil
	}
	return d.fingerprint(dirPath)
}

func (d dirMatcher) nameMatches(name string) bool {
	if d.name != "" && d.name == name {
		return true
	}
	for _, n := range d.nameAny {
		if n == name {
			return true
		}
	}
	return false
}

// anyFileMatching returns a fingerprint that's true if any non-hidden file
// in dirPath (not the directory itself) matches glob.
func anyFileMatching(glob string) func(string) (bool, error) {
	return func(dirPath string) (bool, error) {
		found := false
		err := filepath.WalkDir(dirPath, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if p == dirPath {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			ok, _ := filepath.Match(glob, filepath.Base(p))
			if ok && !strings.HasPrefix(filepath.Base(p), ".") {
				found = true
				return filepath.SkipAll
			}
			return nil
		})
		if found {
			return true, nil
		}
		return false, err
	}
}

func anyFileMatchingOneOf(globs ...string) func(string) (bool, error) {
	return func(dirPath string) (bool, error) {
		for _, g := range globs {
			ok, err := anyFileMatching(g)(dirPath)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
}

func anySubdirOf(names ...string) func(string) (bool, error) {
	return func(dirPath string) (bool, error) {
		for _, n := range names {
			fi, err := os.Stat(filepath.Join(dirPath, n))
			if err == nil && fi.IsDir() {
				return true, nil
			}
		}
		return false, nil
	}
}

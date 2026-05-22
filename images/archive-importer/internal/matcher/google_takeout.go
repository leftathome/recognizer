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
		Name:              "google-takeout",
		UmbrellaMediaType: "archive/google-takeout",
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
		// Subtree matchers. Because we already verified the provider via
		// the 'Takeout/' wrapper directory, the per-service directory
		// names below are canonical Google Takeout output names -- the
		// dirname IS the fingerprint. Each matcher only requires the dir
		// to be non-empty (handled by the default anyContent fingerprint
		// in dirMatcher.Matches) so an accidentally-empty stub doesn't
		// fire a false-positive event.
		//
		// Where the older 2026-04 Takeout dir name differs from the
		// current name (Location History -> Timeline, Chat -> Google Chat),
		// nameAny lets one matcher cover both.
		Subtrees: []SubtreeMatcher{
			// Original 14 (pre-2026-04-Takeout-survey).
			dirMatcher{name: "Mail", mt: "archive/google-takeout/mail", desc: "Gmail export"},
			dirMatcher{name: "Calendar", mt: "archive/google-takeout/calendar", desc: "Google Calendar export"},
			dirMatcher{nameAny: []string{"Chat", "Google Chat"}, mt: "archive/google-takeout/chat", desc: "Google Chat export"},
			dirMatcher{name: "Keep", mt: "archive/google-takeout/keep", desc: "Google Keep export"},
			dirMatcher{name: "NotebookLM", mt: "archive/google-takeout/notebooklm", desc: "NotebookLM export"},
			dirMatcher{name: "Voice", mt: "archive/google-takeout/voice", desc: "Google Voice export"},
			dirMatcher{name: "My Activity", mt: "archive/google-takeout/my-activity", desc: "Google My Activity"},
			dirMatcher{nameAny: []string{"Google Photos"}, mt: "archive/google-takeout/photos", desc: "Google Photos export"},
			dirMatcher{nameAny: []string{"Location History", "Timeline"}, mt: "archive/google-takeout/timeline", desc: "Google location timeline"},
			dirMatcher{nameAny: []string{"YouTube and YouTube Music"}, mt: "archive/google-takeout/youtube", desc: "YouTube + Music export"},
			dirMatcher{name: "Fit", mt: "archive/google-takeout/fit", desc: "Google Fit"},
			dirMatcher{name: "Drive", mt: "archive/google-takeout/drive", desc: "Google Drive"},
			dirMatcher{name: "Tasks", mt: "archive/google-takeout/tasks", desc: "Google Tasks"},
			dirMatcher{name: "Contacts", mt: "archive/google-takeout/contacts", desc: "Google Contacts"},

			// Added 2026-05-21 from a real 260 MB Takeout that surfaced
			// 32 unrecognized subtrees. Sorted alphabetically by the
			// Google directory name for ease of audit.
			dirMatcher{name: "Alerts", mt: "archive/google-takeout/alerts", desc: "Google Alerts subscriptions"},
			dirMatcher{nameAny: []string{"Android Device Configuration Service"}, mt: "archive/google-takeout/android-device-config", desc: "Android device configuration service backup"},
			dirMatcher{name: "Assignments", mt: "archive/google-takeout/classroom", desc: "Google Classroom assignments"},
			dirMatcher{name: "Blogger", mt: "archive/google-takeout/blogger", desc: "Blogger posts + comments"},
			dirMatcher{name: "Chrome", mt: "archive/google-takeout/chrome", desc: "Chrome bookmarks, history, extensions"},
			dirMatcher{name: "Discover", mt: "archive/google-takeout/discover", desc: "Google Discover preferences"},
			dirMatcher{name: "Flow", mt: "archive/google-takeout/flow", desc: "Google Flow"},
			dirMatcher{name: "Gemini", mt: "archive/google-takeout/gemini", desc: "Gemini conversation history"},
			dirMatcher{nameAny: []string{"Google Account"}, mt: "archive/google-takeout/account", desc: "Account-level data"},
			dirMatcher{nameAny: []string{"Google Ads"}, mt: "archive/google-takeout/ads", desc: "Google Ads data"},
			dirMatcher{nameAny: []string{"Google Business Profile"}, mt: "archive/google-takeout/business-profile", desc: "Google Business Profile"},
			dirMatcher{nameAny: []string{"Google Feedback"}, mt: "archive/google-takeout/feedback", desc: "Submitted product feedback"},
			dirMatcher{nameAny: []string{"Google Finance"}, mt: "archive/google-takeout/finance", desc: "Google Finance watchlists"},
			dirMatcher{nameAny: []string{"Google Meet"}, mt: "archive/google-takeout/meet", desc: "Google Meet meeting records"},
			dirMatcher{nameAny: []string{"Google Pay"}, mt: "archive/google-takeout/pay", desc: "Google Pay transactions"},
			dirMatcher{nameAny: []string{"Google Play Books"}, mt: "archive/google-takeout/play-books", desc: "Google Play Books library"},
			dirMatcher{nameAny: []string{"Google Play Movies & TV", "Google Play Movies and TV"}, mt: "archive/google-takeout/play-movies-tv", desc: "Google Play Movies & TV"},
			dirMatcher{nameAny: []string{"Google Play Store"}, mt: "archive/google-takeout/play-store", desc: "Google Play Store apps + reviews"},
			dirMatcher{nameAny: []string{"Google Product Surveys"}, mt: "archive/google-takeout/product-surveys", desc: "Google Product Surveys responses"},
			dirMatcher{nameAny: []string{"Google Shopping"}, mt: "archive/google-takeout/shopping", desc: "Google Shopping order history"},
			dirMatcher{nameAny: []string{"Google Store"}, mt: "archive/google-takeout/store", desc: "Google Store hardware orders"},
			dirMatcher{nameAny: []string{"Google Wallet"}, mt: "archive/google-takeout/wallet", desc: "Google Wallet"},
			dirMatcher{nameAny: []string{"Google Workspace Marketplace"}, mt: "archive/google-takeout/workspace-marketplace", desc: "Workspace Marketplace installations"},
			dirMatcher{name: "Groups", mt: "archive/google-takeout/groups", desc: "Google Groups memberships + posts"},
			dirMatcher{nameAny: []string{"Home App"}, mt: "archive/google-takeout/home", desc: "Google Home App data"},
			dirMatcher{name: "Maps", mt: "archive/google-takeout/maps", desc: "Saved places, reviews, contributions"},
			dirMatcher{name: "Nest", mt: "archive/google-takeout/nest", desc: "Nest device + automation history"},
			dirMatcher{name: "News", mt: "archive/google-takeout/news", desc: "Google News follows + saved articles"},
			dirMatcher{name: "Profile", mt: "archive/google-takeout/profile", desc: "Google account profile"},
			dirMatcher{name: "Saved", mt: "archive/google-takeout/saved", desc: "Saved items from Search and Maps"},
			dirMatcher{nameAny: []string{"Search Contributions"}, mt: "archive/google-takeout/search-contributions", desc: "Search contributions (Q&A, reviews)"},
			dirMatcher{nameAny: []string{"Search Notifications"}, mt: "archive/google-takeout/search-notifications", desc: "Search notifications"},
			dirMatcher{nameAny: []string{"Workspace Studio"}, mt: "archive/google-takeout/workspace-studio", desc: "Workspace Studio"},
		},
	}
}

// dirMatcher implements SubtreeMatcher via directory-name match plus a
// fingerprint over the directory contents.
//
// When fingerprint is nil (the default for canonical Takeout subtree
// names), Matches uses anyContent: the dir must contain at least one
// non-hidden entry. That's strict enough to reject empty stub
// directories but doesn't require knowing the per-service file shape,
// which has changed over time and varies by what services the user
// actually uses (e.g. YouTube's contents are
// {subscriptions,history,playlists,videos,comments,livechat,...} in
// any combination).
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
	fp := d.fingerprint
	if fp == nil {
		fp = anyContent
	}
	return fp(dirPath)
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

// anyContent: dirPath contains at least one non-hidden entry (file or
// subdirectory). Default fingerprint for matchers that trust the dir
// name alone (most canonical Takeout subtrees).
func anyContent(dirPath string) (bool, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			return true, nil
		}
	}
	return false, nil
}

// anyFileMatching returns a fingerprint that's true if any non-hidden file
// in dirPath (not the directory itself) matches glob. Kept for matchers
// that genuinely need content-shape verification.
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

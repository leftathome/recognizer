package matcher

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

// MetaProvider returns the Provider that detects Meta/Facebook data
// exports (the JSON variant) and matches their top-level subtrees.
//
// Detection looks for personal_information/profile_information/profile_information.json
// at the archive root and verifies it contains a profile_v2 key (or the
// older profile_information_v2). The wrapper directory pattern Google
// Takeout uses (Takeout/...) has no equivalent here -- Meta exports the
// 8 known top-level dirs directly at the archive root.
//
// Tested against a real 332 MB facebook-<user>-<date>-<id>.zip with 801
// files (349 JSON + ~430 media + 22 dir/header rows). Instagram export
// support is a separate concern.
func MetaProvider() Provider {
	return Provider{
		Name:              "meta-facebook",
		UmbrellaMediaType: "archive/meta-facebook",
		Detect: func(rootPath string) (bool, string, error) {
			fp := filepath.Join(rootPath, "personal_information", "profile_information", "profile_information.json")
			f, err := os.Open(fp)
			if err != nil {
				if os.IsNotExist(err) {
					return false, "", nil
				}
				return false, "", err
			}
			defer f.Close()
			// Profile JSON is tiny (single object, a few KB). Cap the
			// read so a renamed multi-GB file masquerading as the
			// fingerprint can't OOM the importer.
			head, err := io.ReadAll(io.LimitReader(f, 1<<20))
			if err != nil {
				return false, "", err
			}
			var probe map[string]json.RawMessage
			if err := json.Unmarshal(head, &probe); err != nil {
				return false, "", nil
			}
			if _, ok := probe["profile_v2"]; ok {
				return true, rootPath, nil
			}
			if _, ok := probe["profile_information_v2"]; ok {
				return true, rootPath, nil
			}
			return false, "", nil
		},
		// Depth-1 matchers: one match per top-level dir of the export.
		// your_facebook_activity/ has 18 sub-dirs (posts, messages,
		// events, groups, ...) and personal_information/ has 5; finer
		// per-sub-dir matchers are a follow-up if downstream wants
		// finer routing. For now Glovebox sees one subtree per
		// top-level category.
		Subtrees: []SubtreeMatcher{
			dirMatcher{name: "ads_information", mt: "archive/meta-facebook/ads-information", desc: "Meta ad preferences, advertisers, ad library"},
			dirMatcher{name: "apps_and_websites_off_of_facebook", mt: "archive/meta-facebook/apps-and-websites-off", desc: "Connected apps + off-Facebook activity"},
			dirMatcher{name: "connections", mt: "archive/meta-facebook/connections", desc: "Facebook friends + followers"},
			dirMatcher{name: "logged_information", mt: "archive/meta-facebook/logged-information", desc: "Activity messages, in-app messages, interactions, location, notifications, search"},
			dirMatcher{name: "personal_information", mt: "archive/meta-facebook/personal-information", desc: "Profile info, avatars, accounts center, worlds"},
			dirMatcher{name: "preferences", mt: "archive/meta-facebook/preferences", desc: "Feed, memories, and other preferences"},
			dirMatcher{name: "security_and_login_information", mt: "archive/meta-facebook/security-and-login", desc: "Account activity, logins, device cookies, IP history"},
			dirMatcher{nameAny: []string{"your_facebook_activity"}, mt: "archive/meta-facebook/activity", desc: "Posts, messages, comments, events, groups, marketplace, pages, polls, saved items"},
		},
	}
}

// Reference: top-level Meta export structure validated 2026-05-22
// against a 332 MB facebook-* export. Top-level dirs are:
//   ads_information, apps_and_websites_off_of_facebook, connections,
//   logged_information, personal_information, preferences,
//   security_and_login_information, your_facebook_activity.
// If a fresh user-supplied export surfaces new top-level dirs, add
// dirMatcher entries above rather than letting them go unrecognized.

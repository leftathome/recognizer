// Package matcher defines the archive-layout matcher contract:
// one Provider per archive provider (Google Takeout, future Meta export, etc.)
// containing many SubtreeMatchers that identify per-service subtrees.
//
// See docs/specs/03-archive-importer-google-takeout.md § 5.
package matcher

// SubtreeMatcher identifies a specific archive subtree and reports the
// media_type to emit on a match.
type SubtreeMatcher interface {
	MediaType() string
	Description() string
	Matches(dirPath, dirName string) (bool, error)
}

// Provider groups a top-level provider detector with its subtree matchers.
type Provider struct {
	Name     string
	Detect   func(rootPath string) (matched bool, subtreeBase string, err error)
	Subtrees []SubtreeMatcher
}

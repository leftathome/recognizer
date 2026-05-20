package matcher

import "testing"

type stubMatcher struct {
	mt, desc string
	matches  bool
}

func (s *stubMatcher) MediaType() string   { return s.mt }
func (s *stubMatcher) Description() string { return s.desc }
func (s *stubMatcher) Matches(dirPath, dirName string) (bool, error) {
	return s.matches, nil
}

func TestProvider_HasNameAndSubtrees(t *testing.T) {
	p := Provider{
		Name:   "test",
		Detect: func(string) (bool, string, error) { return true, "/x", nil },
		Subtrees: []SubtreeMatcher{
			&stubMatcher{mt: "archive/test/a", desc: "alpha"},
		},
	}
	if p.Name != "test" {
		t.Errorf("Name lost")
	}
	if len(p.Subtrees) != 1 {
		t.Errorf("Subtrees lost")
	}
	if p.Subtrees[0].MediaType() != "archive/test/a" {
		t.Errorf("MediaType lost")
	}
}

func TestProvider_DetectSignature(t *testing.T) {
	called := false
	p := Provider{
		Detect: func(root string) (bool, string, error) {
			called = true
			if root != "/probe" {
				t.Errorf("Detect got %q, want /probe", root)
			}
			return true, "/probe/Sub", nil
		},
	}
	ok, base, err := p.Detect("/probe")
	if err != nil || !ok || base != "/probe/Sub" || !called {
		t.Errorf("Detect failed contract: ok=%v base=%q err=%v called=%v", ok, base, err, called)
	}
}

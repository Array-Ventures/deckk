package docsend

import "testing"

func TestIsSpaceURL(t *testing.T) {
	cases := map[string]bool{
		"https://docsend.com/view/s/abc123":        true,
		"https://docsend.com/view/s/abc123?x=1":    true,
		"https://docsend.com/view/abc123":          false,
		"https://docsend.com/view/abc123/d/def456": false,
		"https://www.docsend.com/view/sabc123":     false,
		"https://docsend.com/v/abc123/deck":        false,
		"https://docsend.com/view/s":               false,
	}
	for raw, want := range cases {
		if got := isSpaceURL(raw); got != want {
			t.Errorf("isSpaceURL(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestMatches(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://docsend.com/view/abc":           true,
		"https://www.docsend.com/view/abc":       true,
		"https://docsend.dropbox.com/view/abc":   true,
		"https://evildocsend.com/view/abc":       false,
		"https://docsend.com.evil.test/view/abc": false,
		"http://docsend.com/view/abc":            false,
	} {
		if got := (&Adapter{}).Matches(raw); got != want {
			t.Errorf("Matches(%q) = %v; want %v", raw, got, want)
		}
	}
}

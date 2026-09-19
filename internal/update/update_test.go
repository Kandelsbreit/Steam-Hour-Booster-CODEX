package update

import (
	"context"
	"encoding/json"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, test := range []struct {
		candidate, current string
		want               bool
	}{{"2.1.1", "2.1.0", true}, {"v2.2.0", "2.1.9", true}, {"2.1.0", "2.1.0", false}, {"2.0.9", "2.1.0", false}} {
		if got := newer(test.candidate, test.current); got != test.want {
			t.Fatalf("newer(%q,%q)=%v", test.candidate, test.current, got)
		}
	}
}

func TestCheckReportsReleaseWithoutDownloading(t *testing.T) {
	c := New()
	c.request = func(_ context.Context, _ string, _ any, out any) (int, error) {
		payload, _ := json.Marshal(map[string]any{"tag_name": "v2.3.1", "html_url": "https://example.invalid/release"})
		_ = json.Unmarshal(payload, out)
		return 200, nil
	}
	s, err := c.Check(context.Background())
	if err != nil || !s.Available || s.URL == "" || s.Checking {
		t.Fatal(s, err)
	}
}

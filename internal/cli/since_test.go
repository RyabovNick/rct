package cli

import (
	"testing"
	"time"
)

func TestParseSince(t *testing.T) {
	ref := time.Date(2026, 10, 30, 12, 0, 0, 0, time.Local)
	for in, want := range map[string]time.Time{
		"2026-10-01": time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local),
		"7d":         ref.AddDate(0, 0, -7),
		"2w":         ref.AddDate(0, 0, -14),
		"48h":        ref.Add(-48 * time.Hour),
	} {
		got, err := ParseSince(in, ref)
		if err != nil || !got.Equal(want) {
			t.Errorf("%s: got %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "yesterday", "-3d", "-1h"} {
		if _, err := ParseSince(bad, ref); Code(err) != ExitUsage {
			t.Errorf("%q: err %v", bad, err)
		}
	}
}

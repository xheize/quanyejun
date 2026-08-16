package domain

import (
	"strings"
	"testing"
)

func TestNormalizeDisplayName(t *testing.T) {
	if got := NormalizeDisplayName("  루멘\n  하나  "); got != "루멘 하나" {
		t.Fatalf("normalized name = %q", got)
	}
	if got := NormalizeDisplayName("  "); got != DefaultDisplayName {
		t.Fatalf("blank name = %q", got)
	}
	if got := NormalizeDisplayName(strings.Repeat("가", 45)); len([]rune(got)) != 40 {
		t.Fatalf("long name rune count = %d", len([]rune(got)))
	}
}

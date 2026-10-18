package contract

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestModelV2EscapedCharacterBoundaries(t *testing.T) {
	for _, budget := range []int{11999, 12000, 12001} {
		for _, unit := range []string{"x", "中文", "💡", "\"\\\n<>&\u2028"} {
			text := strings.Repeat(unit, 13000)
			prefix := JSONTextPrefix(text, budget)
			chars, err := JSONChars(prefix)
			if err != nil || chars-2 > budget || !utf8.ValidString(prefix) || !strings.HasPrefix(text, prefix) {
				t.Fatalf("prefix budget %d: chars=%d error=%v", budget, chars, err)
			}
			suffix := JSONTextSuffix(text, budget)
			chars, err = JSONChars(suffix)
			if err != nil || chars-2 > budget || !utf8.ValidString(suffix) || !strings.HasSuffix(text, suffix) {
				t.Fatalf("suffix budget %d: chars=%d error=%v", budget, chars, err)
			}
		}
	}
}

func TestModelVisibleBudgetKeepsBodyIndependentFromControl(t *testing.T) {
	if ModelVisibleBodyChars != 12000 {
		t.Fatalf("body budget = %d, want 12000", ModelVisibleBodyChars)
	}
	if ModelVisibleTotalChars != ModelVisibleBodyChars+ModelVisibleControlChars {
		t.Fatalf("total budget = %d, body=%d control=%d", ModelVisibleTotalChars, ModelVisibleBodyChars, ModelVisibleControlChars)
	}
	if ModelVisibleTotalChars <= ModelVisibleBodyChars {
		t.Fatal("control fields must not consume the 12,000-character body budget")
	}
}

package workoverview

import (
	"strings"
	"testing"
)

func TestValidateIDRejectsTerminalAndResourceControlCharacters(t *testing.T) {
	invalid := []string{
		"",
		"run/1",
		"run 1",
		"run\t1",
		"run\n1",
		"run\x1b[2J",
		"run\u00851",
		strings.Repeat("x", 256),
	}
	for _, id := range invalid {
		if err := ValidateID(id); err == nil {
			t.Errorf("ValidateID(%q) = nil, want an error", id)
		}
	}
	if err := ValidateID("review-123"); err != nil {
		t.Fatalf("ValidateID(valid) = %v", err)
	}
}

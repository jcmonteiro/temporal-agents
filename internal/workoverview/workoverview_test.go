package workoverview

import (
	"strings"
	"testing"
)

func TestValidateIDUsesThePublishedUnicodeLength(t *testing.T) {
	if err := ValidateID(strings.Repeat("é", 200)); err != nil {
		t.Fatalf("ValidateID(200 code points) = %v, want a schema-valid ID", err)
	}
	if err := ValidateID(strings.Repeat("é", 256)); err == nil {
		t.Fatal("ValidateID(256 code points) = nil, want an over-length error")
	}
}

func TestValidateItemEnforcesTheActiveWorkContract(t *testing.T) {
	valid := []Item{
		{ID: "run-1", Kind: KindRun, Status: StatusInProgress, Running: true},
		{ID: "schedule-1", Kind: KindSchedule, Status: StatusDone},
	}
	for _, item := range valid {
		if err := ValidateItem(item); err != nil {
			t.Errorf("ValidateItem(%+v) = %v, want valid", item, err)
		}
	}
	invalid := []Item{
		{ID: "run-1\nforged", Kind: KindRun, Status: StatusInProgress, Running: true},
		{ID: "run-1", Kind: Kind("job"), Status: StatusInProgress, Running: true},
		{ID: "run-1", Kind: KindRun, Status: Status("unknown"), Running: true},
		{ID: "run-1", Kind: KindRun, Status: StatusDone},
		{ID: "schedule-1", Kind: KindSchedule, Status: StatusDone, Running: true},
	}
	for _, item := range invalid {
		if err := ValidateItem(item); err == nil {
			t.Errorf("ValidateItem(%+v) = nil, want an error", item)
		}
	}
}

func TestValidateIDRejectsTerminalAndResourceControlCharacters(t *testing.T) {
	invalid := []string{
		"",
		"run/1",
		"run 1",
		"run\t1",
		"run\n1",
		"run\x1b[2J",
		"run\u00851",
		"run\u00a01",
		"run\u202e1",
		"run\ufeff1",
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

package notify

import (
	"context"
	"errors"
	"testing"

	"temporal-agents/internal/notification"
)

// fakeNotifier records the notifications it receives and optionally fails.
type fakeNotifier struct {
	got []notification.Notification
	err error
}

func (f *fakeNotifier) Notify(_ context.Context, n notification.Notification) error {
	f.got = append(f.got, n)
	return f.err
}

func TestMultiDeliversToEveryNotifier(t *testing.T) {
	a, b := &fakeNotifier{}, &fakeNotifier{}
	n := notification.Notification{Title: "done", Body: "all good"}

	if err := (Multi{a, b}).Notify(context.Background(), n); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for name, f := range map[string]*fakeNotifier{"a": a, "b": b} {
		if len(f.got) != 1 || f.got[0] != n {
			t.Errorf("notifier %s got %v, want one %v", name, f.got, n)
		}
	}
}

func TestMultiDeliversToAllEvenWhenOneFails(t *testing.T) {
	boom := errors.New("boom")
	failing := &fakeNotifier{err: boom}
	ok := &fakeNotifier{}

	err := (Multi{failing, ok}).Notify(context.Background(), notification.Notification{Title: "t"})

	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want it to wrap %v", err, boom)
	}
	if len(ok.got) != 1 {
		t.Errorf("second notifier was not called despite the first failing: %v", ok.got)
	}
}

func TestMultiEmptyIsNoOp(t *testing.T) {
	if err := (Multi{}).Notify(context.Background(), notification.Notification{}); err != nil {
		t.Fatalf("empty Multi should be a no-op, got %v", err)
	}
}

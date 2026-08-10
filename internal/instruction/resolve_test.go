package instruction_test

import (
	"context"
	"errors"
	"testing"

	"temporal-agents/internal/instruction"
	"temporal-agents/internal/scoped/scopedtest"
)

// The chain is the whole feature: a value set once covers everything under it, and a
// place that says something of its own is what wins. Each case sets what the row
// says and asserts which scope answered, so the table is the contract of "who wins".
//
// The gap cases matter most: a chain is resolved per key, so a worktree that
// overrides one key and a repository that overrides another must not blend into one
// another, and a scope in the middle that says nothing must be passed over rather
// than treated as an empty answer.
func TestAValueResolvesThroughThePlaceItRunsInBeforeAnythingBroader(t *testing.T) {
	const worktree, repository = "/src/agents-feature", "/src/agents"
	cases := []struct {
		name string
		// stored is what has been set, in the order it was set.
		stored      map[instruction.Scope]string
		wantText    string
		wantScope   instruction.Scope
		wantVersion int
	}{
		{
			name:        "nothing is configured anywhere",
			wantText:    factoryOf(t, instruction.KeyReviewPerform),
			wantScope:   instruction.FactoryScope,
			wantVersion: 0,
		},
		{
			name:        "only the shipped default is published",
			stored:      map[instruction.Scope]string{instruction.FactoryScope: "shipped"},
			wantText:    "shipped",
			wantScope:   instruction.FactoryScope,
			wantVersion: 1,
		},
		{
			name: "the installation overrides the shipped default",
			stored: map[instruction.Scope]string{
				instruction.FactoryScope: "shipped",
				instruction.GlobalScope:  "everywhere",
			},
			wantText:    "everywhere",
			wantScope:   instruction.GlobalScope,
			wantVersion: 1,
		},
		{
			name: "the repository covers the worktree that says nothing",
			stored: map[instruction.Scope]string{
				instruction.FactoryScope:               "shipped",
				instruction.GlobalScope:                "everywhere",
				instruction.DirectoryScope(repository): "for this repository",
			},
			wantText:    "for this repository",
			wantScope:   instruction.DirectoryScope(repository),
			wantVersion: 1,
		},
		{
			name: "the worktree speaks for itself",
			stored: map[instruction.Scope]string{
				instruction.FactoryScope:               "shipped",
				instruction.GlobalScope:                "everywhere",
				instruction.DirectoryScope(repository): "for this repository",
				instruction.DirectoryScope(worktree):   "for this worktree",
			},
			wantText:    "for this worktree",
			wantScope:   instruction.DirectoryScope(worktree),
			wantVersion: 1,
		},
		{
			name: "a gap in the middle of the chain is passed over",
			stored: map[instruction.Scope]string{
				instruction.FactoryScope:             "shipped",
				instruction.DirectoryScope(worktree): "",
			},
			wantText:    "shipped",
			wantScope:   instruction.FactoryScope,
			wantVersion: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := scopedtest.New()
			for scope, text := range tc.stored {
				if text == "" {
					// A scope that has versions but no pointer: reset to what it inherits.
					store.Set(instruction.KeyReviewPerform, scope, "an experiment that was reset")
					store.Clear(instruction.KeyReviewPerform, scope)
					continue
				}
				store.Set(instruction.KeyReviewPerform, scope, text)
			}
			activity := &instruction.Activity{Store: store}

			resolution, err := activity.Resolve(context.Background(), instruction.Request{
				Keys:   []instruction.Key{instruction.KeyReviewPerform},
				Scopes: instruction.Chain(worktree, repository),
			})

			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			value, ok := resolution.Value(instruction.KeyReviewPerform)
			if !ok {
				t.Fatalf("nothing resolved for %s", instruction.KeyReviewPerform)
			}
			if value.Text != tc.wantText || value.Scope != tc.wantScope || value.Version != tc.wantVersion {
				t.Fatalf("resolved %q from %s v%d, want %q from %s v%d",
					value.Text, value.Scope, value.Version, tc.wantText, tc.wantScope, tc.wantVersion)
			}
			if value.Hash != instruction.Hash(tc.wantText) {
				t.Fatalf("the recorded hash does not match the resolved text")
			}
		})
	}
}

// Resolution is per key, never per bundle: a place that overrides one instruction
// keeps inheriting the rest. Resolving both keys at once is what a unit of work
// does, so the two must not travel together.
func TestOverridingOneInstructionLeavesTheOthersInherited(t *testing.T) {
	const worktree = "/src/agents"
	store := scopedtest.New()
	store.Set(instruction.KeyReviewPerform, instruction.GlobalScope, "review everywhere")
	store.Set(instruction.KeyReviewPerform, instruction.DirectoryScope(worktree), "review here")
	store.Set(instruction.KeyReviewImplement, instruction.GlobalScope, "implement {{.Review}} everywhere")
	activity := &instruction.Activity{Store: store}

	resolution, err := activity.Resolve(context.Background(), instruction.Request{
		Keys:   []instruction.Key{instruction.KeyReviewPerform, instruction.KeyReviewImplement},
		Scopes: instruction.Chain(worktree, ""),
	})

	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	perform, _ := resolution.Value(instruction.KeyReviewPerform)
	implement, _ := resolution.Value(instruction.KeyReviewImplement)
	if perform.Scope != instruction.DirectoryScope(worktree) {
		t.Fatalf("the overridden key resolved from %s", perform.Scope)
	}
	if implement.Scope != instruction.GlobalScope {
		t.Fatalf("the inherited key resolved from %s, want the installation's value", implement.Scope)
	}
}

// A store that cannot answer must fail the unit of work, visibly. Answering from the
// build's defaults instead would change what the agent is told with nothing in the
// record to say it happened.
func TestResolutionFailsRatherThanSubstitutingADefault(t *testing.T) {
	outage := errors.New("connection refused")
	store := scopedtest.New()
	store.Err = outage

	_, err := (&instruction.Activity{Store: store}).Resolve(context.Background(), instruction.Request{
		Keys: []instruction.Key{instruction.KeyReviewPerform},
	})

	if !errors.Is(err, outage) {
		t.Fatalf("Resolve = %v, want the store's failure", err)
	}
}

// A worker wired without a store is a misconfiguration, and it has to look like one
// at the first resolution rather than like agent behaviour nobody can explain.
func TestAWorkerWithoutAStoreCannotResolveAnything(t *testing.T) {
	_, err := (&instruction.Activity{}).Resolve(context.Background(), instruction.Request{
		Keys: []instruction.Key{instruction.KeyReviewPerform},
	})

	if !errors.Is(err, instruction.ErrNotConfigured) {
		t.Fatalf("Resolve = %v, want the not-configured failure", err)
	}
}

// A key this build does not govern cannot be answered with anything: a stale stored
// row or a caller's typo must not resolve to some other key's text.
func TestAKeyThisBuildDoesNotGovernIsRefused(t *testing.T) {
	_, err := (&instruction.Activity{Store: scopedtest.New()}).Resolve(context.Background(),
		instruction.Request{Keys: []instruction.Key{"review.invented"}})

	if !errors.Is(err, instruction.ErrUnknownKey) {
		t.Fatalf("Resolve = %v, want the unknown-key refusal", err)
	}
}

// Publishing runs at every startup, so it must add a version only when the shipped
// text actually changed — otherwise the version history would grow by one per
// restart and "which version produced this run?" would stop meaning anything.
func TestPublishingTheShippedDefaultsTwiceAddsNoVersion(t *testing.T) {
	store := scopedtest.New()
	ctx := context.Background()

	if err := instruction.PublishDefaults(ctx, store); err != nil {
		t.Fatalf("first publication: %v", err)
	}
	if err := instruction.PublishDefaults(ctx, store); err != nil {
		t.Fatalf("second publication: %v", err)
	}

	for _, key := range instruction.Keys() {
		if versions := store.Versions(key, instruction.FactoryScope); len(versions) != 1 {
			t.Fatalf("%s has %d factory version(s) after two publications, want 1", key, len(versions))
		}
	}
}

// An upgrade that improves a shipped instruction has to reach every place that never
// overrode it, and it does so by appending a version rather than by rewriting the
// one earlier runs recorded.
func TestAChangedShippedDefaultIsPublishedAsANewVersion(t *testing.T) {
	store := scopedtest.New()
	ctx := context.Background()
	if _, err := store.PublishFactory(ctx, instruction.KeyReviewPerform, "the previous release's wording"); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if err := instruction.PublishDefaults(ctx, store); err != nil {
		t.Fatalf("publish the defaults: %v", err)
	}

	versions := store.Versions(instruction.KeyReviewPerform, instruction.FactoryScope)
	if len(versions) != 2 {
		t.Fatalf("%d version(s) after the upgrade, want the old one kept and the new one added", len(versions))
	}
	if versions[0].Text != "the previous release's wording" {
		t.Fatalf("the version a finished run referenced was rewritten: %q", versions[0].Text)
	}
	resolution, err := (&instruction.Activity{Store: store}).Resolve(ctx, instruction.Request{
		Keys: []instruction.Key{instruction.KeyReviewPerform},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if value, _ := resolution.Value(instruction.KeyReviewPerform); value.Version != 2 {
		t.Fatalf("resolution uses v%d, want the upgraded default", value.Version)
	}
}

// An execution that predates stored instructions carries no resolution at all, and
// it must keep behaving exactly as it did: the prompt it renders is the one the
// build ships.
func TestAnExecutionWithoutAResolutionStillRendersTheShippedInstruction(t *testing.T) {
	rendered, err := instruction.Render(nil, instruction.KeyReviewImplement,
		instruction.Data{"Review": "the review output"})

	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want, err := instruction.Render(instruction.Resolution{{
		Key:  instruction.KeyReviewImplement,
		Text: factoryOf(t, instruction.KeyReviewImplement),
	}}, instruction.KeyReviewImplement, instruction.Data{"Review": "the review output"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if rendered != want {
		t.Fatalf("an execution without a resolution renders %q, want the shipped %q", rendered, want)
	}
}

// factoryOf is the shipped default of one key, so a test states what it expects
// without copying the text.
func factoryOf(t *testing.T, key instruction.Key) string {
	t.Helper()
	spec, ok := instruction.SpecFor(key)
	if !ok {
		t.Fatalf("the catalogue does not govern %s", key)
	}
	return spec.Factory
}

package instruction_test

import (
	"context"
	"errors"
	"testing"

	"temporal-agents/internal/instruction"
	"temporal-agents/internal/scoped/scopedtest"
)

func TestConfigurationShowsTheEffectiveAndInheritedInstructionForAPlace(t *testing.T) {
	store := scopedtest.New()
	ctx := context.Background()
	requirePublishedInstructions(t, ctx, store)
	store.Store(instruction.KeyReviewPerform, instruction.GlobalScope, "review everywhere")
	store.Store(instruction.KeyReviewPerform, instruction.DirectoryScope("/src/agents"), "review this repository")
	configuration := &instruction.Configuration{Store: store}

	catalogue, err := configuration.Catalogue(ctx, instruction.PlaceTarget("/src/agents-feature", "/src/agents"))

	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}
	item := configuredInstruction(t, catalogue, instruction.KeyReviewPerform)
	if item.Effective.Text != "review this repository" || item.Effective.Scope != instruction.DirectoryScope("/src/agents") {
		t.Fatalf("effective = %+v, want the repository override", item.Effective)
	}
	if item.Inherited.Text != "review this repository" || item.Inherited.Scope != instruction.DirectoryScope("/src/agents") {
		t.Fatalf("inherited = %+v, want the repository override", item.Inherited)
	}
	if item.Overridden {
		t.Fatal("a worktree with no own value is reported as overridden")
	}
	if item.Spec.Key == "" || item.Spec.Purpose == "" {
		t.Fatalf("catalogue metadata is incomplete: %+v", item.Spec)
	}
}

func TestSavingAndResettingAPlaceOverrideChangesOnlyThatKey(t *testing.T) {
	store := scopedtest.New()
	ctx := context.Background()
	requirePublishedInstructions(t, ctx, store)
	store.Store(instruction.KeyReviewPerform, instruction.GlobalScope, "review everywhere")
	store.Store(instruction.KeyReviewImplement, instruction.GlobalScope, "implement {{.Review}} everywhere")
	configuration := &instruction.Configuration{Store: store}
	target := instruction.PlaceTarget("/src/agents", "")

	saved, err := configuration.Set(ctx, target, instruction.KeyReviewPerform, "review this repository", "operator-1")
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if saved.SavedBy != "operator-1" {
		t.Fatalf("SavedBy = %q, want operator-1", saved.SavedBy)
	}
	catalogue, err := configuration.Catalogue(ctx, target)
	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}
	perform := configuredInstruction(t, catalogue, instruction.KeyReviewPerform)
	if !perform.Overridden || perform.Effective.Text != "review this repository" || perform.Inherited.Text != "review everywhere" {
		t.Fatalf("perform after save = %+v", perform)
	}
	implement := configuredInstruction(t, catalogue, instruction.KeyReviewImplement)
	if implement.Overridden || implement.Effective.Text != "implement {{.Review}} everywhere" {
		t.Fatalf("another key changed with the override: %+v", implement)
	}

	if err := configuration.Reset(ctx, target, instruction.KeyReviewPerform); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	catalogue, err = configuration.Catalogue(ctx, target)
	if err != nil {
		t.Fatalf("Catalogue after reset: %v", err)
	}
	perform = configuredInstruction(t, catalogue, instruction.KeyReviewPerform)
	if perform.Overridden || perform.Effective.Text != "review everywhere" {
		t.Fatalf("perform after reset = %+v", perform)
	}
}

func TestResettingTheGlobalOverrideReturnsToTheCurrentFactoryInstruction(t *testing.T) {
	store := scopedtest.New()
	ctx := context.Background()
	requirePublishedInstructions(t, ctx, store)
	configuration := &instruction.Configuration{Store: store}

	if _, err := configuration.Set(ctx, instruction.GlobalTarget(), instruction.KeyReviewPerform,
		"review everywhere", "operator-1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := configuration.Reset(ctx, instruction.GlobalTarget(), instruction.KeyReviewPerform); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	catalogue, err := configuration.Catalogue(ctx, instruction.GlobalTarget())
	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}
	item := configuredInstruction(t, catalogue, instruction.KeyReviewPerform)
	spec, _ := instruction.SpecFor(instruction.KeyReviewPerform)
	if item.Overridden || item.Effective.Text != spec.Factory || item.Effective.Scope != instruction.FactoryScope {
		t.Fatalf("after global reset = %+v, want the current factory instruction", item)
	}
}

func TestARefusedSaveLeavesThePreviousVersionEffective(t *testing.T) {
	store := scopedtest.New()
	ctx := context.Background()
	requirePublishedInstructions(t, ctx, store)
	configuration := &instruction.Configuration{Store: store}
	target := instruction.GlobalTarget()
	valid := "implement this review:\n{{.Review}}"
	if _, err := configuration.Set(ctx, target, instruction.KeyReviewImplement, valid, "operator-1"); err != nil {
		t.Fatalf("Set valid: %v", err)
	}

	_, err := configuration.Set(ctx, target, instruction.KeyReviewImplement,
		"implement every finding", "operator-1")

	if !errors.Is(err, instruction.ErrInvalidText) {
		t.Fatalf("Set invalid = %v, want an invalid instruction", err)
	}
	catalogue, catalogueErr := configuration.Catalogue(ctx, target)
	if catalogueErr != nil {
		t.Fatalf("Catalogue: %v", catalogueErr)
	}
	item := configuredInstruction(t, catalogue, instruction.KeyReviewImplement)
	if item.Effective.Text != valid || item.Effective.Version != 1 {
		t.Fatalf("effective after refusal = %+v, want the previous version", item.Effective)
	}
}

func requirePublishedInstructions(t *testing.T, ctx context.Context, store *scopedtest.Store) {
	t.Helper()
	if err := instruction.PublishDefaults(ctx, store); err != nil {
		t.Fatalf("PublishDefaults: %v", err)
	}
}

func configuredInstruction(t *testing.T, catalogue instruction.Catalogue, key instruction.Key) instruction.Configured {
	t.Helper()
	item, ok := catalogue.Value(key)
	if !ok {
		t.Fatalf("catalogue has no %s", key)
	}
	return item
}

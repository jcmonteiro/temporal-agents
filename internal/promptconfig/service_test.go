package promptconfig_test

import (
	"context"
	"errors"
	"testing"

	"temporal-agents/internal/instruction"
	"temporal-agents/internal/promptconfig"
	"temporal-agents/internal/scoped/scopedtest"
)

type placesFake struct {
	places map[string]promptconfig.Place
	err    error
}

func (p placesFake) Find(_ context.Context, id string) (promptconfig.Place, error) {
	if p.err != nil {
		return promptconfig.Place{}, p.err
	}
	place, ok := p.places[id]
	if !ok {
		return promptconfig.Place{}, promptconfig.ErrPlaceNotFound
	}
	return place, nil
}

func TestARegisteredWorktreeResolvesThroughItsRepository(t *testing.T) {
	store := scopedtest.New()
	ctx := context.Background()
	if err := instruction.PublishDefaults(ctx, store); err != nil {
		t.Fatalf("PublishDefaults: %v", err)
	}
	store.Store(instruction.KeyReviewPerform, instruction.DirectoryScope("/src/agents"), "review the repository")
	service := promptconfig.Service{
		Configuration: &instruction.Configuration{Store: store},
		Places: placesFake{places: map[string]promptconfig.Place{
			"worktree-id": {Directory: "/src/agents-feature", Repository: "/src/agents"},
		}},
	}

	catalogue, err := service.Catalogue(ctx, "worktree-id")

	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}
	item, _ := catalogue.Value(instruction.KeyReviewPerform)
	if item.Effective.Text != "review the repository" || item.Overridden {
		t.Fatalf("worktree configuration = %+v", item)
	}
}

func TestAnUnknownPlaceIsRefusedWithoutInventingAScope(t *testing.T) {
	service := promptconfig.Service{
		Configuration: &instruction.Configuration{Store: scopedtest.New()},
		Places:        placesFake{places: map[string]promptconfig.Place{}},
	}

	_, err := service.Catalogue(context.Background(), "unknown-id")

	if !errors.Is(err, promptconfig.ErrPlaceNotFound) {
		t.Fatalf("Catalogue = %v, want no such place", err)
	}
}

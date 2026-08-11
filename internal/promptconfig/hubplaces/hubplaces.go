// Package hubplaces adapts the hub's registered locations to prompt configuration's
// opaque place lookup port.
package hubplaces

import (
	"context"
	"fmt"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/promptconfig"
)

// Registry is the narrow registered-place read the adapter needs.
type Registry interface {
	RegisteredPlaces(ctx context.Context) ([]agenthub.RegisteredPlace, error)
}

// Adapter resolves one location ID from the registered-place catalogue.
type Adapter struct {
	Registry Registry
}

func (a Adapter) Find(ctx context.Context, locationID string) (promptconfig.Place, error) {
	if a.Registry == nil {
		return promptconfig.Place{}, fmt.Errorf("registered place lookup is not configured")
	}
	places, err := a.Registry.RegisteredPlaces(ctx)
	if err != nil {
		return promptconfig.Place{}, err
	}
	for _, registered := range places {
		if registered.Location.ID() != locationID {
			continue
		}
		directory, ok := registered.Location.Directory()
		if !ok {
			return promptconfig.Place{}, fmt.Errorf("%w: %s", promptconfig.ErrPlaceNotFound, locationID)
		}
		place := promptconfig.Place{Directory: directory}
		if parent, hasParent := registered.Location.Parent(); hasParent {
			place.Repository, _ = parent.Directory()
		}
		return place, nil
	}
	return promptconfig.Place{}, fmt.Errorf("%w: %s", promptconfig.ErrPlaceNotFound, locationID)
}

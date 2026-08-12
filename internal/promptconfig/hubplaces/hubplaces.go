// Package hubplaces adapts the hub's known locations to prompt configuration's
// opaque place lookup port.
package hubplaces

import (
	"context"
	"fmt"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/promptconfig"
)

// Registry is the narrow known-place read the adapter needs.
type Registry interface {
	KnownPlaces(ctx context.Context) ([]agenthub.KnownPlace, error)
}

// Adapter resolves one location ID from the known-place catalogue.
type Adapter struct {
	Registry Registry
}

func (a Adapter) Find(ctx context.Context, locationID string) (promptconfig.Place, error) {
	if a.Registry == nil {
		return promptconfig.Place{}, fmt.Errorf("known place lookup is not configured")
	}
	places, err := a.Registry.KnownPlaces(ctx)
	if err != nil {
		return promptconfig.Place{}, err
	}
	for _, known := range places {
		if known.Location.ID() != locationID {
			continue
		}
		directory, ok := known.Location.Directory()
		if !ok {
			return promptconfig.Place{}, fmt.Errorf("%w: %s", promptconfig.ErrPlaceNotFound, locationID)
		}
		place := promptconfig.Place{Directory: directory}
		if parent, hasParent := known.Location.Parent(); hasParent {
			place.Repository, _ = parent.Directory()
		}
		return place, nil
	}
	return promptconfig.Place{}, fmt.Errorf("%w: %s", promptconfig.ErrPlaceNotFound, locationID)
}

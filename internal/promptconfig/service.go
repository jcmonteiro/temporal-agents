// Package promptconfig coordinates prompt configuration with the registered place
// catalogue. It is an application service: instruction owns validation and
// inheritance, the place adapter resolves opaque IDs, and this package joins them
// without exposing either adapter to the other core.
package promptconfig

import (
	"context"
	"errors"
	"fmt"

	"temporal-agents/internal/instruction"
)

var (
	// ErrPlaceNotFound reports a location ID that is not a registered editable place.
	ErrPlaceNotFound = errors.New("prompt configuration place not found")
	// ErrUnavailable marks a driven dependency that could not answer.
	ErrUnavailable = errors.New("prompt configuration unavailable")
)

// Place is the established scope chain behind one opaque location ID.
type Place struct {
	Directory  string
	Repository string
}

// Places resolves an opaque location ID to probed place facts.
type Places interface {
	Find(ctx context.Context, locationID string) (Place, error)
}

// Service publishes and mutates instruction configuration globally or for one
// registered place. An empty location ID means the global scope.
type Service struct {
	Configuration *instruction.Configuration
	Places        Places
}

func (s *Service) Catalogue(ctx context.Context, locationID string) (instruction.Catalogue, error) {
	target, err := s.target(ctx, locationID)
	if err != nil {
		return nil, err
	}
	catalogue, err := s.Configuration.Catalogue(ctx, target)
	if err != nil {
		return nil, unavailable(err)
	}
	return catalogue, nil
}

func (s *Service) Set(ctx context.Context, locationID string, key instruction.Key, text, savedBy string) (instruction.Record, error) {
	target, err := s.target(ctx, locationID)
	if err != nil {
		return instruction.Record{}, err
	}
	record, err := s.Configuration.Set(ctx, target, key, text, savedBy)
	if err != nil {
		if errors.Is(err, instruction.ErrInvalidText) || errors.Is(err, instruction.ErrUnknownKey) {
			return instruction.Record{}, err
		}
		return instruction.Record{}, unavailable(err)
	}
	return record, nil
}

// SetModel saves the Pi model selector paired with one governed instruction.
func (s *Service) SetModel(ctx context.Context, locationID string, key instruction.Key, model, savedBy string) (instruction.Record, error) {
	target, err := s.target(ctx, locationID)
	if err != nil {
		return instruction.Record{}, err
	}
	record, err := s.Configuration.SetModel(ctx, target, key, model, savedBy)
	if err != nil {
		if errors.Is(err, instruction.ErrInvalidText) || errors.Is(err, instruction.ErrUnknownKey) {
			return instruction.Record{}, err
		}
		return instruction.Record{}, unavailable(err)
	}
	return record, nil
}

// ResetModel returns one agent model selector to its inherited value.
func (s *Service) ResetModel(ctx context.Context, locationID string, key instruction.Key) error {
	target, err := s.target(ctx, locationID)
	if err != nil {
		return err
	}
	if err := s.Configuration.ResetModel(ctx, target, key); err != nil {
		if errors.Is(err, instruction.ErrUnknownKey) {
			return err
		}
		return unavailable(err)
	}
	return nil
}

func (s *Service) Reset(ctx context.Context, locationID string, key instruction.Key) error {
	target, err := s.target(ctx, locationID)
	if err != nil {
		return err
	}
	if err := s.Configuration.Reset(ctx, target, key); err != nil {
		if errors.Is(err, instruction.ErrUnknownKey) {
			return err
		}
		return unavailable(err)
	}
	return nil
}

func (s *Service) target(ctx context.Context, locationID string) (instruction.Target, error) {
	if s == nil || s.Configuration == nil {
		return instruction.Target{}, fmt.Errorf("%w: instruction configuration is not configured", ErrUnavailable)
	}
	if locationID == "" {
		return instruction.GlobalTarget(), nil
	}
	if s.Places == nil {
		return instruction.Target{}, fmt.Errorf("%w: place lookup is not configured", ErrUnavailable)
	}
	place, err := s.Places.Find(ctx, locationID)
	if err != nil {
		if errors.Is(err, ErrPlaceNotFound) {
			return instruction.Target{}, err
		}
		return instruction.Target{}, unavailable(err)
	}
	if place.Directory == "" {
		return instruction.Target{}, fmt.Errorf("%w: %s", ErrPlaceNotFound, locationID)
	}
	return instruction.PlaceTarget(place.Directory, place.Repository), nil
}

func unavailable(err error) error {
	if errors.Is(err, ErrUnavailable) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrUnavailable, err)
}

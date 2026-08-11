package instruction

import (
	"context"
	"errors"
	"fmt"

	"temporal-agents/internal/scoped"
)

// ErrInvalidTarget marks a configuration scope that the operator cannot address.
// Only the installation and one established directory place are editable. The
// factory is published by the build and is never an operator-owned pointer.
var ErrInvalidTarget = errors.New("invalid instruction configuration target")

// Target is one editable scope and the resolution chain that starts there. The
// constructors are the public way to build one, so callers do not reproduce scope
// ordering.
type Target struct {
	scope  Scope
	scopes []Scope
}

// GlobalTarget addresses the installation-wide override. Its inherited value is the
// current factory value.
func GlobalTarget() Target {
	return Target{scope: GlobalScope, scopes: []Scope{GlobalScope, FactoryScope}}
}

// PlaceTarget addresses one directory place. repository is present only for a
// linked worktree and comes from the location probe, never from path inference.
func PlaceTarget(directory, repository string) Target {
	return Target{scope: DirectoryScope(directory), scopes: Chain(directory, repository)}
}

// Scope reports the scope the operator is editing.
func (t Target) Scope() Scope { return t.scope }

// Configured is one catalogue item as it applies at a target.
type Configured struct {
	Spec       Spec
	Effective  Value
	Inherited  Value
	Overridden bool
}

// Catalogue is every governed instruction in stable catalogue order.
type Catalogue []Configured

// Value finds one configured instruction by key.
func (c Catalogue) Value(key Key) (Configured, bool) {
	for _, item := range c {
		if item.Spec.Key == key {
			return item, true
		}
	}
	return Configured{}, false
}

// Configuration is the application core for instruction reads and mutations. It
// owns validation and inheritance; its driven store owns only append-only versions
// and pointers.
type Configuration struct {
	Store scoped.Store
}

// Catalogue returns effective and inherited values for every governed key. Both
// answers are computed by the same winner rule used by running work.
func (c *Configuration) Catalogue(ctx context.Context, target Target) (Catalogue, error) {
	if c == nil || c.Store == nil {
		return nil, ErrNotConfigured
	}
	if err := validateTarget(target); err != nil {
		return nil, err
	}
	keys := Keys()
	records, err := c.Store.Current(ctx, keys, target.scopes)
	if err != nil {
		return nil, fmt.Errorf("read instruction configuration: %w", err)
	}
	inheritedScopes := target.scopes[1:]
	catalogue := make(Catalogue, 0, len(keys))
	for _, spec := range Specs() {
		effective := resolve(spec, target.scopes, records)
		inherited := resolve(spec, inheritedScopes, records)
		catalogue = append(catalogue, Configured{
			Spec:       spec,
			Effective:  effective,
			Inherited:  inherited,
			Overridden: effective.Scope == target.scope,
		})
	}
	return catalogue, nil
}

// Set validates and appends one override, recording the principal responsible.
func (c *Configuration) Set(ctx context.Context, target Target, key Key, text, savedBy string) (Record, error) {
	if c == nil || c.Store == nil {
		return Record{}, ErrNotConfigured
	}
	if err := validateTarget(target); err != nil {
		return Record{}, err
	}
	spec, ok := SpecFor(key)
	if !ok {
		return Record{}, fmt.Errorf("%w: %s", ErrUnknownKey, key)
	}
	if err := spec.Validate(text); err != nil {
		return Record{}, err
	}
	record, err := c.Store.Set(ctx, key, target.scope, text, savedBy)
	if err != nil {
		return Record{}, fmt.Errorf("save the %s instruction: %w", key, err)
	}
	return record, nil
}

// Reset clears one override pointer. It is idempotent: resetting an already
// inherited key has the same successful result and creates no version.
func (c *Configuration) Reset(ctx context.Context, target Target, key Key) error {
	if c == nil || c.Store == nil {
		return ErrNotConfigured
	}
	if err := validateTarget(target); err != nil {
		return err
	}
	if _, ok := SpecFor(key); !ok {
		return fmt.Errorf("%w: %s", ErrUnknownKey, key)
	}
	if err := c.Store.Reset(ctx, key, target.scope); err != nil {
		return fmt.Errorf("reset the %s instruction: %w", key, err)
	}
	return nil
}

func validateTarget(target Target) error {
	if target.scope == "" || target.scope == FactoryScope || len(target.scopes) < 2 || target.scopes[0] != target.scope {
		return fmt.Errorf("%w: the target must be global or an established directory place", ErrInvalidTarget)
	}
	if target.scope != GlobalScope && target.scope.Kind() != "directory" {
		return fmt.Errorf("%w: %s cannot be edited", ErrInvalidTarget, target.scope)
	}
	if target.scopes[len(target.scopes)-1] != FactoryScope {
		return fmt.Errorf("%w: the target has no factory fallback", ErrInvalidTarget)
	}
	return nil
}

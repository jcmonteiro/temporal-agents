package setting_test

import (
	"context"
	"errors"
	"testing"

	"temporal-agents/internal/instruction"
	"temporal-agents/internal/scoped"
	"temporal-agents/internal/scoped/scopedtest"
	"temporal-agents/internal/setting"
)

// A setting obeys exactly the inheritance an instruction does — that is the whole
// reason it shares the mechanism — so this table is the same shape as the
// instruction chain's, stated for a typed value. Each case sets what is stored and
// asserts both the answer and where it came from, because an interface shows the
// second and must never re-derive it.
func TestASettingResolvesThroughThePlaceItIsAskedForBeforeAnythingBroader(t *testing.T) {
	const worktree, repository = "/src/agents-feature", "/src/agents"
	cases := []struct {
		name      string
		stored    map[scoped.Scope]string
		want      bool
		wantScope scoped.Scope
	}{
		{
			name:      "nothing is configured anywhere",
			want:      true,
			wantScope: setting.FactoryScope,
		},
		{
			name:      "only the shipped default is published",
			stored:    map[scoped.Scope]string{setting.FactoryScope: "true"},
			want:      true,
			wantScope: setting.FactoryScope,
		},
		{
			name: "the installation switches it on",
			stored: map[scoped.Scope]string{
				setting.FactoryScope: "false",
				setting.GlobalScope:  "true",
			},
			want:      true,
			wantScope: setting.GlobalScope,
		},
		{
			name: "the repository covers the worktree that says nothing",
			stored: map[scoped.Scope]string{
				setting.FactoryScope:               "false",
				setting.DirectoryScope(repository): "true",
			},
			want:      true,
			wantScope: setting.DirectoryScope(repository),
		},
		{
			name: "the worktree speaks for itself",
			stored: map[scoped.Scope]string{
				setting.FactoryScope:               "false",
				setting.GlobalScope:                "true",
				setting.DirectoryScope(repository): "true",
				setting.DirectoryScope(worktree):   "false",
			},
			want:      false,
			wantScope: setting.DirectoryScope(worktree),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := scopedtest.New()
			for scope, text := range tc.stored {
				store.Store(setting.KeySteeringEnabled, scope, text)
			}
			resolver := &setting.Resolver{Store: store}

			resolution, err := resolver.Resolve(context.Background(), setting.Request{
				Scopes: setting.Chain(worktree, repository),
			})

			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			value, ok := resolution.Value(setting.KeySteeringEnabled)
			if !ok {
				t.Fatalf("nothing resolved for %s", setting.KeySteeringEnabled)
			}
			if value.Enabled != tc.want || value.Scope != tc.wantScope {
				t.Fatalf("resolved %v from %s, want %v from %s",
					value.Enabled, value.Scope, tc.want, tc.wantScope)
			}
			if inherited := value.Inherited(worktree); inherited != (tc.wantScope != setting.DirectoryScope(worktree)) {
				t.Fatalf("Inherited = %v for a value from %s", inherited, value.Scope)
			}
		})
	}
}

// A caller that asks for nothing in particular gets every governed setting, which is
// what a configuration surface needs, and each one carries its own source: resolution
// is per key, never per bundle.
func TestAskingForNothingResolvesEveryGovernedSetting(t *testing.T) {
	store := scopedtest.New()
	store.Store(setting.KeySteeringEnabled, setting.GlobalScope, "true")

	resolution, err := (&setting.Resolver{Store: store}).Resolve(context.Background(), setting.Request{})

	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolution) != len(setting.Keys()) {
		t.Fatalf("resolved %d setting(s), want every one of the %d governed", len(resolution), len(setting.Keys()))
	}
	if !resolution.Enabled(setting.KeySteeringEnabled) {
		t.Fatal("the installation's value was not used")
	}
}

// A value that cannot be read back as the type its key means must fail the read, not
// quietly become the default: one bad row would otherwise change what the tool does
// while every surface reported the default as if somebody had chosen it.
func TestAStoredValueThatCannotBeReadBackFailsTheResolution(t *testing.T) {
	store := scopedtest.New()
	store.Store(setting.KeySteeringEnabled, setting.GlobalScope, "yes")

	_, err := (&setting.Resolver{Store: store}).Resolve(context.Background(), setting.Request{})

	if !errors.Is(err, setting.ErrInvalidValue) {
		t.Fatalf("Resolve = %v, want a refusal naming the value", err)
	}
}

// The same check is what saving runs, so a value that could not be read back can
// never be stored in the first place. The refusal names the key and what is
// accepted.
func TestOnlyAValueTheKeyCanMeanMayBeSaved(t *testing.T) {
	spec, ok := setting.SpecFor(setting.KeySteeringEnabled)
	if !ok {
		t.Fatalf("the catalogue does not govern %s", setting.KeySteeringEnabled)
	}
	for _, refused := range []string{"", "yes", "1", "TRUE", "off"} {
		if err := spec.Validate(refused); !errors.Is(err, setting.ErrInvalidValue) {
			t.Fatalf("Validate(%q) = %v, want a refusal", refused, err)
		}
	}
	for _, accepted := range []string{"true", "false"} {
		if err := spec.Validate(accepted); err != nil {
			t.Fatalf("Validate(%q) = %v, want it accepted", accepted, err)
		}
	}
}

// A store that cannot answer fails the caller. This is the same promise instruction
// resolution makes, for the same reason: a silent default changes behaviour with
// nothing in the record to say so.
func TestResolutionFailsRatherThanSubstitutingADefault(t *testing.T) {
	outage := errors.New("connection refused")
	store := scopedtest.New()
	store.Err = outage

	_, err := (&setting.Resolver{Store: store}).Resolve(context.Background(), setting.Request{})

	if !errors.Is(err, outage) {
		t.Fatalf("Resolve = %v, want the store's failure", err)
	}
}

// Publishing runs at every startup, so it must add a version only when the shipped
// default actually changed.
func TestPublishingTheShippedSettingsTwiceAddsNoVersion(t *testing.T) {
	store := scopedtest.New()
	ctx := context.Background()

	if err := setting.PublishDefaults(ctx, store); err != nil {
		t.Fatalf("first publication: %v", err)
	}
	if err := setting.PublishDefaults(ctx, store); err != nil {
		t.Fatalf("second publication: %v", err)
	}

	for _, key := range setting.Keys() {
		if versions := store.Versions(key, setting.FactoryScope); len(versions) != 1 {
			t.Fatalf("%s has %d factory version(s) after two publications, want 1", key, len(versions))
		}
	}
}

// Settings and instructions share one key space, because they share one storage
// discipline: a key that appeared in both catalogues would be two values with one
// name, and whichever wrote last would silently win.
func TestNoKeyIsGovernedByTwoCatalogues(t *testing.T) {
	settings := map[scoped.Key]bool{}
	for _, key := range setting.Keys() {
		settings[key] = true
	}
	for _, key := range instructionKeys() {
		if settings[key] {
			t.Fatalf("%s is governed as both a setting and an instruction", key)
		}
	}
}

// instructionKeys is the other catalogue's key space, read from it rather than
// copied, so a key added there is checked here without anybody remembering to.
func instructionKeys() []scoped.Key { return instruction.Keys() }

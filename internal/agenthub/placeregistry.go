package agenthub

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The places an operator registered, as opposed to the places observed from work
// that ran.
//
// A place with no work in it cannot be discovered by observation: nothing recorded
// it, so nothing publishes it, so an operator cannot pick it — and the first run in
// a repository would be impossible from the hub. Registration is how an operator
// says "the hub may work here" before any work exists there.
//
// What an operator supplies is a directory and nothing else. Where that directory
// sits — whether it is a working tree of its own inside a repository — is a probed
// fact, taken from the same probe the recorded places come from, so a registration
// can never state a hierarchy that contradicts what the system can establish. The
// registration is therefore stored as a RecordedPlace: the same kind of fact a run
// records, from the same source.

// ErrNoSuchDirectory is returned when nothing is at the directory an operator
// named. It is separate from ErrNotARepository because the two are different
// mistakes with different fixes — a typo, against a directory that is not under
// version control — and an operator told only "that will not do" has to guess which.
var ErrNoSuchDirectory = errors.New("no such directory")

// ErrNotARepository is returned when the directory exists but no repository holds
// it. The hub works by branching, committing and reviewing, so a directory no
// repository holds is a place it cannot work in at all.
var ErrNotARepository = errors.New("not a repository")

// maxPlaceDirectoryLength bounds what may be submitted, mirroring the bound a
// location itself carries.
const maxPlaceDirectoryLength = maxLocationPathLength

// PlaceRegistration is one registered place as it is stored: the facts the probe
// established about the directory, plus who asked for it and when.
//
// The identity is the probed directory. An operator who names a subdirectory of a
// working tree registers that working tree, so naming the same place in two ways
// registers it once.
type PlaceRegistration struct {
	// Place is what the probe established: the working tree, and the repository it
	// belongs to when the two genuinely differ.
	Place RecordedPlace
	// RegisteredAt is when the place was first registered. A repeat registration
	// does not move it.
	RegisteredAt time.Time
	// RegisteredBy identifies the principal who registered it, and is empty on a
	// deployment that authenticates nobody. It is recorded for audit only: nothing
	// is filtered by it.
	RegisteredBy string
}

// RegisteredPlace is one registered place as the core publishes it: the place
// itself, and the provenance of the registration.
type RegisteredPlace struct {
	// Location is the place, derived from the probed facts by the core, so no
	// consumer turns a directory into a place of its own accord.
	Location Location
	// RegisteredAt is when it was first registered.
	RegisteredAt time.Time
	// RegisteredBy is which principal registered it, empty where nobody is
	// authenticated.
	RegisteredBy string
}

// PlaceStore is the driven port for the places an operator registered. It is a
// port of its own, next to the dismissal store rather than inside any read port,
// so the read path stays read-only by construction.
type PlaceStore interface {
	// Registrations returns every registered place.
	Registrations(ctx context.Context) ([]PlaceRegistration, error)
	// Register stores a registration and returns the stored one. It must be
	// idempotent on the registration's identity — the probed directory — so a repeat
	// returns the original registration rather than creating a second one or failing.
	Register(ctx context.Context, registration PlaceRegistration) (PlaceRegistration, error)
}

// PlaceInspector is the driven port that answers what a directory an operator
// named actually is. It is the registration's half of the same question the
// location probe answers for running work, and it is a port so the core never
// learns that git and a filesystem are what answer it.
//
// An implementation reports ErrNoSuchDirectory when nothing is there and
// ErrNotARepository when no repository holds it, so a refusal can name the
// mistake instead of describing it vaguely.
type PlaceInspector interface {
	// Inspect reports the facts about directory.
	Inspect(ctx context.Context, directory string) (RecordedPlace, error)
}

// ValidatePlaceDirectory checks what an operator submitted, before anything on the
// server's filesystem is touched.
//
// The rules are the location's own — absolute, cleaned, bounded, printable — but the
// failures are ErrInvalid rather than ErrInvalidLocation: this value came from a
// request, so the consumer is the one who can fix it, and saying so is not a leak
// of a server path (the consumer sent it).
func ValidatePlaceDirectory(directory string) error {
	switch {
	case directory == "":
		return fmt.Errorf("%w: the directory is required", ErrInvalid)
	case strings.TrimSpace(directory) != directory:
		return fmt.Errorf("%w: the directory %q must not be padded with whitespace", ErrInvalid, directory)
	case !utf8.ValidString(directory):
		return fmt.Errorf("%w: the directory is not valid text", ErrInvalid)
	case utf8.RuneCountInString(directory) > maxPlaceDirectoryLength:
		return fmt.Errorf("%w: the directory must be at most %d characters", ErrInvalid, maxPlaceDirectoryLength)
	case strings.IndexFunc(directory, unicode.IsControl) >= 0:
		return fmt.Errorf("%w: the directory contains control characters", ErrInvalid)
	case !strings.HasPrefix(directory, "/"):
		return fmt.Errorf("%w: the directory %q must be an absolute path", ErrInvalid, directory)
	case path.Clean(directory) != directory:
		return fmt.Errorf("%w: the directory %q must be written plainly, as %q",
			ErrInvalid, directory, path.Clean(directory))
	default:
		return nil
	}
}

// RegisteredPlaces returns every place an operator registered, in a stable order.
//
// They are published whether or not any work has ever run in them: a place that
// was registered and never used is exactly the case registration exists for, and
// leaving it out until something runs there would defeat it.
func (s *Service) RegisteredPlaces(ctx context.Context) ([]RegisteredPlace, error) {
	registrations, err := s.deps.Places.Registrations(ctx)
	if err != nil {
		return nil, unavailable("read the registered places", err)
	}
	places := make([]RegisteredPlace, 0, len(registrations))
	for _, registration := range registrations {
		place, err := placeFrom(registration)
		if err != nil {
			return nil, err
		}
		places = append(places, place)
	}
	sort.SliceStable(places, func(i, j int) bool {
		return places[i].Location.ID() < places[j].Location.ID()
	})
	return places, nil
}

// RegisterPlace records that the hub may work in a directory, and returns the place
// it registered.
//
// The directory is checked against the request rules first and then against the
// machine, because the two refusals mean different things: a relative path is a
// request to fix, while a directory that is not there is a fact about the server.
// What is stored is what the probe answered, never what was typed: an operator who
// names a subdirectory registers the working tree that holds it, and the parent a
// worktree hangs under is the repository the probe named and nothing else.
//
// Registering the same place again succeeds and returns the original registration,
// so a retried request, a double click and a second operator all end with one place.
func (s *Service) RegisterPlace(ctx context.Context, directory, by string) (RegisteredPlace, error) {
	if err := ValidatePlaceDirectory(directory); err != nil {
		return RegisteredPlace{}, err
	}
	facts, err := s.deps.Inspector.Inspect(ctx, directory)
	switch {
	case errors.Is(err, ErrNoSuchDirectory), errors.Is(err, ErrNotARepository):
		return RegisteredPlace{}, err
	case err != nil:
		return RegisteredPlace{}, unavailable("inspect "+directory, err)
	case !facts.Recorded():
		// A probe that answers nothing has established nothing, and a place must never
		// be invented from what was typed.
		return RegisteredPlace{}, fmt.Errorf("%w: %s", ErrNotARepository, directory)
	}
	stored, err := s.deps.Places.Register(ctx, PlaceRegistration{
		Place:        facts,
		RegisteredAt: s.deps.Now().UTC(),
		RegisteredBy: by,
	})
	if err != nil {
		return RegisteredPlace{}, unavailable("register the place", err)
	}
	return placeFrom(stored)
}

// placeFrom derives the published place from a stored registration. A stored fact
// that cannot be expressed as a location is a defect here, not a bad request, so it
// keeps ErrInvalidLocation.
func placeFrom(registration PlaceRegistration) (RegisteredPlace, error) {
	location, err := registration.Place.Location()
	if err != nil {
		return RegisteredPlace{}, err
	}
	return RegisteredPlace{
		Location:     location,
		RegisteredAt: registration.RegisteredAt,
		RegisteredBy: registration.RegisteredBy,
	}, nil
}

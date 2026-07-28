package piagent

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The pi model catalog (`pi --list-models`) is stable for the lifetime of a
// worker process, so it is parsed once and cached. Resolving a context window
// is then a cheap map lookup, keyed by the provider + model display name that
// pi reports on each assistant message.

var (
	catalogOnce sync.Once
	catalog     map[string]int // provider\x00model -> context window in tokens
)

// colSplit splits a `pi --list-models` row into columns. The table is
// whitespace-aligned with at least two spaces between columns, and the model
// column itself may contain single spaces (e.g. "Claude Opus 4.8").
var colSplit = regexp.MustCompile(`\s{2,}`)

// contextWindowFor returns the context-window size in tokens for the given
// provider/model, or 0 when it cannot be resolved (unknown model or catalog
// unavailable). Safe for concurrent use.
func contextWindowFor(provider, model string) int {
	warmContextWindows()
	if catalog == nil {
		return 0
	}
	return catalog[catalogKey(provider, model)]
}

// warmContextWindows populates the catalog cache once. Subsequent calls are
// no-ops, so it is cheap to call eagerly to hide the catalog subprocess latency
// behind other startup work.
func warmContextWindows() {
	catalogOnce.Do(func() {
		catalog = loadContextWindows()
	})
}

// catalogTimeout bounds the pi model catalog subprocess. Because
// contextWindowFor waits on the same sync.Once, a hung lookup would otherwise
// block JSON-stream processing and heartbeats; the timeout keeps percentage
// enrichment best-effort so a stuck catalog cannot stall a healthy activity.
const catalogTimeout = 10 * time.Second

// loadContextWindows runs the pi model catalog and parses each row into a
// provider/model -> context-window map. --offline avoids network calls; the
// locally cached catalog is sufficient for context-window sizes. The lookup is
// bounded by catalogTimeout and returns nil on timeout or error, degrading
// gracefully to absolute-only token reporting.
func loadContextWindows() map[string]int {
	ctx, cancel := context.WithTimeout(context.Background(), catalogTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pi", "--offline", "--list-models").Output()
	if err != nil {
		return nil
	}
	return parseContextWindows(string(out))
}

// parseContextWindows parses the `pi --list-models` table into a
// provider/model -> context-window map. Rows whose context column is not a
// recognizable size (including the header) are skipped.
func parseContextWindows(table string) map[string]int {
	m := make(map[string]int)
	for _, line := range strings.Split(table, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := colSplit.Split(strings.TrimSpace(line), -1)
		if len(cols) < 3 {
			continue
		}
		window := parseTokenSize(cols[2])
		if window <= 0 {
			continue
		}
		m[catalogKey(cols[0], cols[1])] = window
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

func catalogKey(provider, model string) string {
	return provider + "\x00" + model
}

// parseTokenSize converts a catalog size like "200K", "1M", or "128K" into a
// token count. Returns 0 when the input is not a recognizable size.
func parseTokenSize(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	mult := 1
	switch s[len(s)-1] {
	case 'K', 'k':
		mult, s = 1_000, s[:len(s)-1]
	case 'M', 'm':
		mult, s = 1_000_000, s[:len(s)-1]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int(v * float64(mult))
}

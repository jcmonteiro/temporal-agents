BINARY := temporal-agents
PLAYWRIGHT_INSTALL_ARGS ?= chromium

.PHONY: build install uninstall setup fmt lint test test-go test-web

# Build the binary into the current directory.
build:
	go build -o $(BINARY) .

# Resolve the install directory: GOBIN if set, otherwise GOPATH/bin.
GOBIN_DIR = $${GOBIN:-$$(go env GOPATH)/bin}

# Install the binary globally. Make sure the install dir is on your PATH.
install:
	go install .
	@dir=$(GOBIN_DIR); \
		echo "installed '$(BINARY)' to $$dir"; \
		case ":$$PATH:" in \
			*":$$dir:"*) ;; \
			*) echo "note: $$dir is not on your PATH; add it, e.g.:"; \
			   echo "  export PATH=\"$$dir:\$$PATH\"" ;; \
		esac

# Remove the globally installed binary.
uninstall:
	@dir=$(GOBIN_DIR); rm -f "$$dir/$(BINARY)" && echo "removed '$(BINARY)' from $$dir"

# Enable the versioned git hooks (formats Go code on commit).
setup:
	git config core.hooksPath .githooks
	@echo "git hooks enabled (.githooks)"

fmt:
	gofmt -w .

# Check formatting and run the Go static analyzer without changing files.
lint:
	@test -z "$$(gofmt -l .)" || { \
		echo "The following files are not gofmt-formatted:"; \
		gofmt -l .; \
		exit 1; \
	}
	go vet ./...

# Run every test suite that CI runs: web unit tests, browser-based Storybook
# tests, and Go tests. Playwright downloads Chromium only when it is missing.
# CI overrides PLAYWRIGHT_INSTALL_ARGS to also install browser system packages.
test: test-web test-go

test-web:
	pnpm --dir web install --frozen-lockfile
	pnpm --dir web test
	pnpm --dir web exec playwright install $(PLAYWRIGHT_INSTALL_ARGS)
	pnpm --dir web test:storybook

# Run every Go test, integration suites included. The execstore adapter suite
# starts its own throwaway Postgres with testcontainers-go, so it needs a running
# Docker daemon but no setup, no environment variable and no compose service —
# and it cannot skip itself, so a green run really did exercise the SQL.
#
# The flags are the ones CI uses: -race catches the data races a workflow's
# concurrent activities can introduce, and -shuffle=on catches tests that
# depend on their order. The execstore and Agent Hub dismissal adapters each
# start their own throwaway Postgres with testcontainers-go.
test-go:
	go test -race -shuffle=on ./...

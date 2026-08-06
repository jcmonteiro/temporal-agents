BINARY := temporal-agents

.PHONY: build install uninstall setup fmt test

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

# Run every test, integration suites included. The execstore adapter suite starts
# its own throwaway Postgres with testcontainers-go, so it needs a running Docker
# daemon but no setup, no environment variable and no compose service — and it
# cannot skip itself, so a green run really did exercise the SQL.
test:
	go test ./...

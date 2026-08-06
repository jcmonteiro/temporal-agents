BINARY := temporal-agents

# The throwaway database the adapter integration suite reaches on the compose
# Postgres. It is deliberately *not* the temporal_agents database the README tells
# you to use for real work: the suite truncates the tables it touches, so pointing
# it at the working database would delete the recorded history and the stored fleet
# plans. The name ends in "_test", which is what the suite itself insists on.
TEST_DB := temporal_agents_test
TEST_DSN := postgres://postgres:postgres@localhost:15432/$(TEST_DB)?sslmode=disable

.PHONY: build install uninstall setup fmt test test-integration

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

# Run every test. The execstore adapter suite needs a real Postgres and skips
# itself when TEST_DATABASE_URL is unset (see test-integration).
test:
	go test ./...

# Run every test including the execstore adapter suite, against a throwaway
# database on the compose Postgres. --wait blocks until the health check passes, so
# a cold volume does not fail the first run, and the database is created on demand
# because compose only creates POSTGRES_DB.
test-integration:
	docker compose up -d --wait postgres
	docker compose exec -T postgres sh -c \
		'psql -U postgres -tAc "SELECT 1 FROM pg_database WHERE datname='\''$(TEST_DB)'\''" | grep -q 1 \
			|| psql -U postgres -c "CREATE DATABASE $(TEST_DB)"'
	TEST_DATABASE_URL="$(TEST_DSN)" go test ./...

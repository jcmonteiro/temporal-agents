## Code Style

Hexagonal Architecture!

## Testing

- Tests follow the definitions of "Unit Testing Principles, Practices, and Patterns - Vladimir Khorikov"
- Integration tests MUST BE developed with testcontainers-go and not docker compose or manually run containers.

### Frontend (`web/`)

- `pnpm test` runs Vitest in shuffled order. `pnpm build` runs `tsc --build && vite build`.
- Pure logic (`layout.ts`, `view.ts`, `clients/mapping.ts`, `domain/`) is unit tested with no test doubles.
- Pages and components are integration tested against the real component tree, with the HTTP API stubbed at the `fetch` edge (`src/test/fake-api.ts`). Never replace the client itself.
- The wire names the client reads are pinned on the Go side by
  `TestOverviewResourcesKeepTheFieldNamesTheWebClientReads`, because `web/src/clients/api.ts` is a hand-written copy of the Go resources.

### Formatting & Imports

- Run `gofmt` (or let the editor handle it). No custom format rules.

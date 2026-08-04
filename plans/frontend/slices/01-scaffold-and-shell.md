# Slice 1 — Scaffold + app shell

**Discharges:** IB §1 (Go coexistence), IB §2 (stack/structure mirror, auth
dropped), IB §5 (Faro + package-manager decisions), IB §6 (seams).

**Demo:** run the dev server from `web/`; the app boots showing the fixed top
bar (logo "Agent Hub", search field, notification bell, avatar) and the left
nav (Overview active; Fleets, Workflows, Templates, Insights, Settings, Help).
The Overview route renders an empty content area with the greeting. `go build .`
and the Go CI still pass, untouched.

## Tasks

- [ ] Create top-level `web/` directory as the frontend package root (IB §1).
      Do **not** nest a second wrapper dir.
- [ ] Add `web/package.json` (private) with React 19, `react-dom`,
      `react-router`, the four `@lego/*` packages, and dev deps: `vite`,
      `@vitejs/plugin-react`, `vite-plugin-svgr`, `typescript`, `@biomejs/biome`,
      `vitest`, `jsdom`, `@testing-library/react`, `@testing-library/jest-dom`,
      `@testing-library/user-event`. Scripts: `dev`, `build`
      (`tsc --build && vite build`), `lint`, `test` (IB §2).
- [ ] Add `web/vite.config.mts` mirroring the reference: `root: 'src'`,
      `outDir: '../dist'`, react + svgr plugins, dev proxy `/api/v1` → backend
      target env var, vitest `environment: 'jsdom'`, `setupFiles`,
      `deps.inline` for CONNECT components (IB §2, §3).
- [ ] Add `web/tsconfig.json`, `web/biome.json`, `web/vite-env.d.ts`.
- [ ] Add `src/index.html`, `src/index.tsx` (theme + utilities CSS imports,
      `ErrorBoundary`, `Router`; **no** MSAL/auth providers — IB §2 dropped),
      `src/styles/global.css`, `src/styles/theme.ts` (constants + status color
      tokens placeholder), `src/styles/favicon`.
- [ ] Add `src/router.tsx`: browser router, `RouterErrorBoundary`, `App` shell,
      lazy Overview route as index. **No** `RequireAuth`/`RequireRole` (IB §2).
- [ ] Add `src/app.tsx` shell: fixed `TopBar` + left nav + `<Outlet/>` content
      area, native HTML + inline `style` only (IB §2 no layout primitives).
- [ ] Add `src/navigation/top-bar.tsx` (logo, search placeholder, bell, avatar)
      and the left nav component with the six destinations + Help, using CONNECT
      components and `@lego/icons`.
- [ ] Add `src/utils/result.ts`, `src/utils/error-boundary.tsx`,
      `src/utils/router-error-boundary.tsx` (ported from reference).
- [ ] Add `src/config/index.ts` with only non-auth config (env, backend base
      url, optional Faro flag) (IB §2 dropped auth fields).
- [ ] Add `src/test/setup.ts` and `src/test/render.tsx` (`renderWithRouter`).
- [ ] Decide + record: drop Faro (default) and pick package manager (IB §5).
- [ ] Update root `.gitignore` to ignore `web/node_modules`, `web/dist`,
      `web/*.tsbuildinfo` (IB §1). Commit the lockfile.
- [ ] Add `.github/workflows/web.yml` (Node install + `lint` + `test` +
      `build`) scoped to `web/**` paths; leave `go.yml` untouched (IB §1, §6).
- [ ] Add `web/AGENTS.md` capturing the carried-over conventions (CONNECT
      quirks in tests, functional components, Result usage).
- [ ] Test: a component test asserts the shell renders the nav destinations and
      marks Overview active.

## Done when

App runs via `dev`, `lint`/`test`/`build` pass in `web/`, `go build .` and Go CI
are unaffected, and no auth code is present.

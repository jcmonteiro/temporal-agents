# Vertical slices — Authentication

| # | Slice | Demo |
|---|-------|------|
| 1 ✅ | [Explicit schema migration](./01-explicit-migration.md) | `migrate` applies the schema; `worker` and `serve` refuse to run against an older schema with a clear message |
| 2 ✅ | [Sign-in through a provider](./02-provider-sign-in.md) | with the local provider running, a browser signs in, gets a session, and reads the API; signing out stops it |
| 3 ✅ | [Frontend sign-in and 401 handling](./03-frontend-sign-in.md) | opening the hub redirects to sign-in, returns to the intended page after, and shows who is signed in |
| 4 | [Closing the door](./04-closing-the-door.md) | no unauthenticated mode remains; cross-site and non-simple-request rules enforced; CLI still works with its token |

✅ = delivered.

Slice 1 is a prerequisite for every later context that owns tables. Slices 2–3 make
sign-in real end to end. Slice 4 removes the old open surface and proves automation
still works.

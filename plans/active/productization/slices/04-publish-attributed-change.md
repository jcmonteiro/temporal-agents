# Slice 04 — Publish an attributed change

## Outcome

A mapped requester can ask Temporal Agents to develop a change and receive a branch
and pull request with fixed, auditable Git and GitHub attribution.

## Attribution policy

```text
Author:     Requesting user, mapped to their GitHub identity
Committer:  Temporal Agents
Pusher:     GitHub App
PR creator: GitHub App
```

## Demonstration

1. Map an authenticated Hub principal to a verified GitHub user.
2. Start a development run against a registered repository.
3. Let the run produce and validate a commit.
4. Publish the branch and create a pull request.
5. Inspect the commit and confirm the mapped user is author and Temporal Agents is
   committer.
6. Inspect GitHub and confirm the GitHub App pushed the branch and created the pull
   request.
7. Inspect the Hub audit record for requester, mapping, app installation, commit, and
   pull-request facts.

## Delivers

- Verified Hub-principal to GitHub-user mapping based on stable GitHub user ID.
- Controlled author name and email selection, with a mapped GitHub `noreply` address
  when no verified commit email is available.
- Fixed Temporal Agents committer identity.
- Trusted commit/checkpoint creation after agent work.
- Just-in-time GitHub App token acquisition for push and GitHub API operations.
- Pull-request creation through a credential-aware GitHub adapter.
- Audit records linking requester, GitHub mapping, app installation, branch, commits,
  and pull request.
- Clear refusal when publication has no valid requester mapping or sufficient app
  permission.

## Constraints discharged

- Request input and agent output cannot choose attribution identity.
- User identity remains distinct from workload authentication.
- Push and pull-request credentials remain outside the Pi process.
- Remote operations remain available for scheduled work after a user signs out.
- Tokens do not appear in remote URLs or command arguments.

## Not included

- Commits authored by an unmapped user.
- Push through a user personal access token.
- Package publication or release signing.
- Support for source-control systems other than GitHub.

## Done when

- Automated tests inspect the resulting commit's author and committer fields.
- An integration test inspects the remote actor for push and pull-request creation.
- Renamed GitHub logins retain mapping through stable user ID.
- Removed or invalid mappings block publication without losing the local result or
  exposing a credential.
- The audit record contains identities and produced object IDs, but no token.

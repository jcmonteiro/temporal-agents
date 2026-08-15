# Slice 02 — Run against a registered GitHub repository

## Outcome

An administrator can connect an installation workload identity, approve a GitHub
repository, and let a requester run non-publishing work against a disposable clone.
No server filesystem path is the repository's product identity.

## Demonstration

1. Configure or connect a GitHub App installation.
2. Select one repository the installation may access.
3. Start a read-only planning or review run from the Hub.
4. Observe a short-lived checkout credential being acquired by a trusted operation.
5. Inspect the result, repository identity, source revision, requester, and workspace
   identity in the Hub.
6. Confirm that the workspace is removed after the run.
7. Inspect Temporal history and durable records and confirm that they contain a
   credential reference, not the installation token.

## Delivers

- Stable GitHub repository registration and repository-level authorization.
- Association between a repository and an approved GitHub App installation.
- Repository selection in the Hub without a host folder picker.
- A workspace reference that is independent of its temporary path.
- Disposable clone allocation and cleanup.
- Just-in-time GitHub installation token acquisition for trusted checkout.
- A first credential-reference and resolution boundary.
- Audit facts for registration, requester, repository, source revision, workspace,
  and GitHub App installation.
- A compatibility decision for existing path-based places and scoped values.

## Constraints discharged

- Repository identity is stable across hosts and runner replacement.
- Raw GitHub credentials do not enter workflow input, event history, prompts, or
  durable run records.
- Background repository access uses installation identity rather than a user's
  browser session or personal token.
- Local path facts remain implementation details, not authorization facts.

## Not included

- Commit publication, push, or pull-request creation.
- User-to-GitHub authorship mapping.
- Private dependency credentials.
- Multiple execution workers.

## Done when

- A registered repository can be used from a clean installation with no local
  checkout prepared by an operator.
- An unregistered repository is refused before a credential is minted.
- A token is limited to the approved installation and repository permissions.
- Token values cannot be found in normal logs, workflow histories, stored execution
  text, notifications, repository remotes, or command arguments.
- Success, failure, cancellation, and timeout all trigger workspace cleanup.
- Repository access and cleanup have integration tests against real external-system
  boundaries or faithful disposable test services.

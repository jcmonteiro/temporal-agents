# Slice 06 — Activate an approved capability

## Outcome

An administrator can activate a pinned, approved capability bundle, configure it
through the Hub, grant only its declared credential references, and use it from a
repository profile without modifying Temporal Agents core code.

## Demonstration

1. Make one reviewed capability bundle available in a runner artifact or approved
   bundle store.
2. Inspect its version, digest, resources, configuration schema, runtime requirements,
   permissions, and credential declarations in the Hub.
3. Activate it for the installation and configure it for one repository profile.
4. Run work that uses its skill or tool and one isolated external integration.
5. Disable the capability and confirm that later runs cannot load or call it.
6. Upgrade to another pinned version and inspect both activation audit and run
   provenance.

## Delivers

- A capability-bundle catalogue with immutable identity and source provenance.
- Configuration schema validation and append-only configured versions.
- Declared credential types, OAuth scopes, network destinations, filesystem needs,
  commands, runner profiles, and Pi compatibility.
- Separate installation and activation operations.
- Explicit loading of approved Pi package resources.
- One out-of-process MCP or equivalent integration boundary using a short-lived
  credential or credential-injecting gateway.
- Trust classification for built-in, organization-approved, and unsupported
  untrusted code.
- Upgrade compatibility and rollback metadata.

## Constraints discharged

- Pi packages are an implementation form, not the product's secret or policy store.
- Runtime npm or Git installation of unreviewed production code is not required.
- An integration receives only declared, approved credential references.
- Disabling a capability changes later runs without rewriting old run provenance.
- Third-party executable code does not receive implicit access to every runner
  credential.

## Not included

- An open public plugin marketplace.
- Arbitrary runtime installation by a requesting user or agent.
- In-process execution of unreviewed extensions.
- Automatic trust of repository-provided executable extensions.

## Done when

- Bundle source and dependencies are pinned and verified before runtime.
- Activation fails if its schema, runtime requirements, Pi compatibility, or required
  credential grants are invalid.
- A run records bundle ID, version, digest, configuration version, and granted
  credential-reference IDs.
- The external integration cannot access an undeclared credential or network
  destination under the selected runner policy.
- Disable and upgrade behaviour is covered by end-to-end tests.

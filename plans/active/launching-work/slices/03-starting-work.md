# Slice 3 — Starting work (port, idempotency, conflict refusal)

**Discharges:** IB §1 (write port, no paths, idempotency, core-level conflict
refusal, attribution), IB §2 (what may be started), IB §5.

**Demo:** a start request for a registered place submits real work that the worker
executes and the overview shows; repeating the same request identity returns the same
run; a second writing loop for the same place is refused naming the conflict; a
request naming a path instead of a place is rejected by the contract.

## Tasks

- [ ] Add the start port in the core, separate from every read port, with the
      supported kinds and their meaningful options only.
- [ ] Resolve the working directory from the registry; the contract has no path
      field.
- [ ] Mint identities so the same caller request identity maps to the same execution,
      and a repeat returns the existing run rather than starting or failing.
- [ ] Implement the concurrency rule in the core: refuse a second conflicting writing
      loop for the same place, with a problem document naming the conflicting run.
- [ ] Record the starting principal and the resolved place on the run.
- [ ] Add the transport route with authentication and the mutation request rules;
      answer with the created run and where to find it.
- [ ] Unit tests (fakes, no orchestrator): kind and option validation, idempotent
      repeat, conflict refusal, unknown place refusal, path field absent from the
      contract.
- [ ] Adapter tests against the orchestrator's test facilities: the submitted input
      matches what the CLI would submit for the same request.

## Done when

Work can be started through the API for a known place only, exactly once per request
identity, never in conflict with an existing loop, and with attribution recorded.

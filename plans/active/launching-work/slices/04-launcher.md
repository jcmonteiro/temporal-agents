# Slice 4 — Launcher on the place page

**Discharges:** brief outcome (start from the hub, place chosen not typed, land on
the run), IB §2 (minimal option surface), IB §6 (no empty-page-as-failure).

**Demo:** on a place page, an operator chooses what to run, submits, and lands on the
new run's page, which shows the run as starting even before the read path lists it.
Submitting twice quickly produces one run. A conflicting start shows the refusal
with a link to the conflicting run.

## Tasks

- [ ] Add the launcher to the place page only (never the overview): choose the kind,
      supply the instruction where the kind needs one, and expose only the options
      that change the shape of the work.
- [ ] Generate the request identity in the client per submission attempt, so retries
      reuse it.
- [ ] Disable and indicate progress during submission; on success navigate to the run
      page; on refusal render the problem inline, with the conflicting run linked.
- [ ] Show the place's directory as context, non-editable.
- [ ] Handle the read-path delay: the run page shows a starting state rather than an
      error while the run is not yet listed.
- [ ] Component tests: one submission per double click, navigation on success,
      refusal rendering with link, options limited to the intended set, and no way to
      type a directory.

## Done when

An operator starts work from a place page with no terminal, cannot mistype a
location, cannot double-submit, and is taken to the run.

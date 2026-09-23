# Rolling Sprint Stanzas

Status: APPROVED

A sprint is one small, demonstrable vertical slice. It need not complete a
feature set. It must leave one runnable proof of its stated behavior.

## Rolling rule

Keep exactly three uncleared sprints in this index.

After work starts, process sprints in order:

1. Clear the active sprint with its acceptance evidence, reflection, and human
   approval.
2. Apply concrete lessons from that sprint to the two remaining sprint records.
3. Add one new tail sprint directory and index entry.

In this project, **clear** means that the sprint's approved behaviors pass, its
full available suite passes, its documentation matches, independent review has
no unresolved blocker, the reflection is complete, and the human accepts the
evidence.

### Required clearance reflection

Record answers and evidence in the cleared sprint's `SPRINT.md`:

1. **Placement:** Should this work have started in the position where it was
   planned?
2. **Continuation:** Should work proceed through the remaining planned sprints
   in their current order?
3. **Predecessors:** Did the sprint reveal work that should precede either
   remaining sprint?
4. **Prior-sprint gaps:** Did a cleared sprint that affected this sprint omit
   required work, risk, evidence, or support?
5. **Tools:** Which required tools, access, fixtures, or automation were missing?
6. **Went well:** What reduced risk, effort, or uncertainty?
7. **Did not go well:** What caused failure, rework, delay, or confusion?
8. **Support:** What human decision, environment change, research, or other
   support is required before another sprint starts?

Apply each concrete effect to the remaining plans. Use `none` when evidence
shows no change. Do not add speculative work.

### Sizing and review checks

- Keep one public success path and only its mandatory trust-boundary failures in
  one sprint. Put independently demonstrable recovery or another trust boundary
  in a later sprint.
- If PPP needs more than 12 numbered behaviors, split the sprint or record a
  human-approved size exception before implementation.
- Preflight language commands in the same working directory and form that hooks
  will use.
- Run destructive or mutation review probes in a disposable copy. Compare the
  worktree status before and after each read-only review; unexpected writes
  invalidate that review evidence until removed and rerun.

Each sprint is a separate Forge run. Before implementation, PPP creates one
`SPEC-S<NN>-<name>.md` inside that sprint directory with EARS-lite behaviors,
pseudocode, decisions, literal bounds, and the expected file surface. TDD starts
only after human approval of that specification.

## Entry gate

Implementation cannot start until:

- this directory is a Git repository with a recorded base commit;
- a dedicated worktree can be created;
- the selected Go toolchain is installed through a version manager and pinned
  in the repository; and
- Forge enforcement hooks pass preflight, or the human approves the documented
  manual-gate variant.

These are environment prerequisites, not a product sprint.

## Planned sprints

1. [S02 — Addressed local delivery](sprints/S02-addressed-local-delivery/SPRINT.md)
2. [S03 — Offline delivery recovery](sprints/S03-offline-delivery-recovery/SPRINT.md)
3. [S04 — Authenticated one-event replication](sprints/S04-authenticated-one-event-replication/SPRINT.md)

## Cleared sprint history

1. [S01 — Signed local message](sprints/S01-signed-local-message/SPRINT.md) — cleared 2026-09-23; [PR #1](https://github.com/qops1981/inter-harness-message-relay/pull/1), merge `5c52428ca408fe9b9fce7680852a84898056cc2f`.

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
4. **Tools:** Which required tools, access, fixtures, or automation were missing?
5. **Went well:** What reduced risk, effort, or uncertainty?
6. **Did not go well:** What caused failure, rework, delay, or confusion?
7. **Support:** What human decision, environment change, research, or other
   support is required before another sprint starts?

Apply each concrete effect to the remaining plans. Use `none` when evidence
shows no change. Do not add speculative work.

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

1. [S01 — Signed local message](sprints/S01-signed-local-message/SPRINT.md)
2. [S02 — Trusted two-peer replication](sprints/S02-trusted-two-peer-replication/SPRINT.md)
3. [S03 — Durable addressed inbox](sprints/S03-durable-addressed-inbox/SPRINT.md)

## Cleared sprint history

None.

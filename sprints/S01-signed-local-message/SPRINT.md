# Sprint S01 — Signed local message

Status: CLEARED 2026-09-23

Merged evidence: PR [#1](https://github.com/qops1981/inter-harness-message-relay/pull/1), merge commit `5c52428ca408fe9b9fce7680852a84898056cc2f`.

## Outcome

One local relay process creates, stores, verifies, and reads one signed
direct-message event through the public local interface.

## Included

- Pin Go 1.26.8 with `mise` and initialize the Go module.
- Create and open one scoped project with one author and one recipient.
- Support initialization, send, history, status, and shutdown through bounded
  strict JSONL and equivalent direct CLI commands.
- Canonicalize with the approved JCS dependency, sign with Ed25519, derive the
  event ID, and publish the immutable event on the local filesystem.
- Verify stored events before returning them from history.
- Emit bounded structured JSON logs on `stderr`.

## Acceptance evidence

- B1–B25 and the real-process create/send/restart/history/tamper proof passed.
- `go test ./...`, `go build ./...`, `go vet ./...`, and
  `go test -race ./...` passed.
- Both fuzz targets passed for 10 seconds with one worker.
- `go mod verify` passed. `govulncheck@v1.8.0 ./...` found no vulnerability.
- Darwin/amd64 and Windows/amd64 cross-builds passed.
- The final correctness, security, and execution review had no unresolved
  finding.
- The developer guide matches the implemented S01 interface.
- Native APFS and NTFS runtime checks were unavailable. The evidence does not
  claim native filesystem validation.

## Not included

Peer networking, reconciliation, artifacts, session queues, claims, presence,
project control events, installers, and harness integrations.

## Plan adjustments

S01 showed that a 25-behavior sprint was still too large after PPP reduced the
first draft from 45 behaviors. S02 now implements only the first delivery
success path. S03 owns offline recovery and redelivery. S04 starts peer
replication with one authenticated event. Later reconciliation recovery remains
a future tail sprint.

## Clearance reflection

1. **Placement:** Yes. Signed immutable local history, strict framing, and one
   shared CLI/core path are prerequisites for delivery and replication.
2. **Continuation:** No. The prior S02 combined transport, trust, transfer,
   gap recovery, restart, retry, duplicate, and fork behavior. The queue is now
   split and reordered to follow `DESIGN.md` §9: local delivery, offline
   delivery, then cross-machine replication.
3. **Predecessors:** No new product prerequisite exists. The corrected S02
   delivery path must precede its S03 recovery behavior, and basic authenticated
   transfer must precede later reconciliation recovery.
4. **Prior-sprint gaps:** None; S01 had no predecessor. All confirmed S01 gaps
   were fixed before merge, including malformed JSON handling, root and managed
   entry checks, bounded markers and status values, cleanup reporting, response
   accounting, exact canonical-envelope responses, non-EOF input faults,
   documentation, and the primary example.
5. **Tools:** Native APFS and NTFS runners were unavailable. The Forge Go row
   initially passed rejected absolute package arguments and was corrected
   upstream. `govulncheck` was absent as a binary, so verification ran the
   pinned reported version with `go run`. Reviewer isolation also failed once:
   one reviewer wrote four scratch files, two sessions retained stale Pi
   extension context, and one execution stream ended without a report.
6. **Went well:** Strict RED/GREEN increments, the B25 subprocess proof,
   dependency isolation, stdlib-first implementation, fuzz/race checks, fresh
   verification, and independent refuters found defects before merge. The
   product stayed in five Go files with one runtime dependency.
7. **Did not go well:** The sprint produced a 6,968-line change and required
   many late verification corrections. Initial JCS vectors missed
   escaped-backslash surrogate text, malformed-input repair, scalar whitespace,
   and later response escaping. Review tooling also caused avoidable rework.
8. **Support:** No new dependency or architecture decision blocks S02. Native
   macOS and Windows filesystem evidence is still required before a release can
   claim those runtime durability properties. Shadow dogfooding remains blocked
   until S03 passes `SPEC.md` §14 and receives separate human approval.

The human accepted the reviewed S01 evidence by directing PR creation and merge
on 2026-09-23.

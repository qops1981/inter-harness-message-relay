# Sprint S01 — Signed local message

Status: PLAN APPROVED; PPP PENDING

## Outcome

One local relay process creates, stores, verifies, and reads one signed
direct-message event through the public local interface.

## Included

- Select and repository-pin the minimum Go version, then initialize the Go
  module. Use the approved version manager from the entry gate.
- Freeze only the JSONL limits and schemas required by this sprint.
- Create one project and epoch with one local author identity and one registered
  recipient identity.
- Support initialization, direct-message send, history read, status, and clean
  shutdown through strict bounded JSONL on stdio.
- Provide equivalent direct CLI commands by calling the same concrete core
  functions.
- Canonicalize with the approved JCS dependency, sign with Ed25519, derive the
  event ID, and publish the immutable event on the local filesystem.
- Verify stored events before returning them from history.
- Emit bounded structured JSON logs on `stderr`.

## Proof

A runnable integration test shall:

1. Create a temporary project and participant identities.
2. Send one direct message through JSONL.
3. Stop and restart the process.
4. Read the identical verified event from history.
5. Reject a stored event whose bytes changed after publication.
6. Show protocol records only on `stdout` and JSON logs only on `stderr`.

The sprint also runs the JCS vectors, `go test ./...`, `go vet ./...`, and
`go test -race ./...`.

## Not included

Peer networking, reconciliation, artifacts, session queues, claims, presence,
project control events, installers, and harness integrations.

## Plan adjustments

None. This is the initial approved plan.

## Clearance

Not cleared.

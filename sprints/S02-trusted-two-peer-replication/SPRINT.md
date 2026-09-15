# Sprint S02 — Trusted two-peer replication

Status: PLAN APPROVED; SUBJECT TO S01 LEARNINGS

## Outcome

Two isolated relay processes exchange and retain signed message events over an
authenticated connection.

## Included

- Configure two peer addresses and exact test trust identities out of band.
- Connect peers with mutual TLS 1.3.
- Exchange per-participant contiguous cursors and request missing event ranges.
- Validate project, epoch, participant trust, signature, sequence, predecessor,
  recipient, and event ID before publication.
- Bound connection input, staged future events, reconnect attempts, and jittered
  backoff.
- Treat an identical event as a duplicate and quarantine a conflicting event at
  the same author sequence.
- Resume reconciliation after process restart or temporary disconnection.

## Proof

A runnable two-process integration test shall:

1. Start peers with separate project directories and trusted test certificates.
2. Send one event on peer A and replicate it to peer B.
3. Disconnect peer B, append another event on peer A, and reconnect peer B.
4. Verify that both peers retain the same two valid events.
5. Deliver a later sequence first and publish it only after its predecessor.
6. Verify idempotent duplicate handling and fork quarantine.
7. Reject an untrusted TLS peer and a modified signed event.

The full Sprint S01 suite remains green.

## Not included

Session delivery queues, claims, artifacts, presence, project control changes,
global discovery, and cross-machine release packaging.

## Plan adjustments

None. Update this section after S01 clearance.

## Clearance

Not cleared.

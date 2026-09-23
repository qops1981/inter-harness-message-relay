# Sprint S04 — Authenticated one-event replication

Status: PLAN APPROVED; SUBJECT TO EARLIER LEARNINGS

## Outcome

Two isolated relay processes transfer and retain one valid signed message event
over one authenticated peer connection.

## Included

- Configure two peer addresses and exact test trust identities out of band.
- Connect peers with mutual TLS 1.3.
- Exchange the minimum bounded cursor and event request needed for one
  contiguous event.
- Validate project, epoch, participant trust, signature, sequence,
  predecessor, recipient, and event ID before publication.
- Publish the verified immutable event through the existing store.
- Treat an identical retransmission as an idempotent duplicate.
- Extend bounded status and sanitized logs for the connection and transfer.

## Proof

A runnable two-process integration test shall:

1. Start peers with separate project directories and trusted test certificates.
2. Send one event on peer A and replicate it to peer B.
3. Restart peer B and read the identical verified event from history.
4. Retransmit the event and observe idempotent success without a second copy.
5. Reject an untrusted TLS peer.
6. Reject a modified signed event without publishing it.

The full S01–S03 suites remain green.

## Not included

Disconnect recovery, persistent reconciliation progress, missing ranges,
out-of-order future-event staging, retry and jittered backoff, fork quarantine,
artifacts, presence, project control, discovery, and release packaging.

## Plan adjustments

Created from the S01 clearance reflection. It is the smallest authenticated
cross-machine success path after the delivery-first S02 and S03 work. A later
tail sprint will add resumable range reconciliation, disorder handling, and
fork quarantine.

## Clearance

Not cleared.

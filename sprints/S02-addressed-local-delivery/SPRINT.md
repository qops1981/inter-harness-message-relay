# Sprint S02 — Addressed local delivery

Status: PLAN APPROVED; SUBJECT TO PPP

## Outcome

Two local session sidecars that use one project history send, claim,
acknowledge, and retain one addressed delivery through the public interface.

## Included

- Add explicit harness and session identities and bind each session to its
  distinct participant identity.
- Derive one addressed delivery from a verified direct-message event.
- Publish complete delivery state on one filesystem through `tmp`, `new`,
  `processing`, and `done` directories.
- Support bounded `poll`, claim, and acknowledgment operations through strict
  JSONL and the direct CLI.
- Keep delivery state derived from and separate from immutable project history.
- Support `addressed` awareness only.
- Extend bounded status and sanitized logs for this success path.

## Proof

A runnable two-session integration test shall:

1. Start two session sidecars against one local project history.
2. Send one signed direct message to the recipient participant.
3. Poll, claim, and acknowledge its delivery through the public interface.
4. Restart the recipient and observe the acknowledged delivery in `done`.
5. Verify that a non-recipient session does not receive the delivery.
6. Verify that project history did not change during claim or acknowledgment.

The full S01 suite remains green.

## Not included

Blocking wait, claim expiry, redelivery, explicit failure, queue rebuild, peer
networking, reconciliation, artifacts, presence, project control, and
installation automation.

## Plan adjustments

Created from the S01 clearance reflection. It replaces the prior replication-
first S02 and follows the delivery-first order in `DESIGN.md` §9. PPP must keep
the first success path separate from S03 recovery behavior.

## Clearance

Not cleared.

# Sprint S03 — Offline delivery recovery

Status: PLAN APPROVED; SUBJECT TO EARLIER LEARNINGS

## Outcome

An offline addressed recipient recovers, claims, redelivers, and acknowledges a
durable direct-message delivery after restart.

## Included

- Add bounded `wait` and explicit delivery-failure operations.
- Add the `failed` delivery state.
- Expire bounded claims and redeliver unacknowledged deliveries.
- Discover messages created while the recipient sidecar was stopped.
- Rebuild derived queue state and its cursor from verified project history
  without changing history.
- Preserve `addressed` awareness and full history queries.
- Extend bounded status and sanitized logs for recovery outcomes.

## Proof

The runnable dogfood activation check from `SPEC.md` §14 shall:

1. Start two isolated session sidecars.
2. Stop the recipient before the sender publishes a signed direct message.
3. Restart the recipient and recover the queued delivery.
4. Wait for and claim the delivery through the public interface.
5. Let one claim expire and observe one redelivery.
6. Acknowledge the redelivery and observe it in `done` after restart.
7. Delete the derived queue and cursor, then restart and observe safe redelivery.
8. Verify that rebuilding did not change durable project history.

The full S01 and S02 suites remain green. Successful evidence permits
shadow-mode dogfooding only after separate human approval.

## Not included

Peer networking, cross-machine reconciliation, broadcast or `all` awareness,
observed traffic, artifacts, presence, work-status events, project control,
installation automation, and harness-specific bridges.

## Plan adjustments

The S01 clearance reflection split the former durable-inbox sprint. S02 owns
only the local claim-and-acknowledge success path; this sprint owns recovery,
time, and rebuild behavior. The §14 activation proof remains intact.

## Clearance

Not cleared.

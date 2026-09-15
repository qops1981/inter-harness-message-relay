# Sprint S03 — Durable addressed inbox

Status: PLAN APPROVED; SUBJECT TO EARLIER LEARNINGS

## Outcome

An addressed recipient receives, claims, acknowledges, and can recover a
replicated direct message after disconnection or restart.

## Included

- File direct events into one session's staged delivery queue.
- Implement `wait`, `poll`, claim, acknowledgment, and explicit failure through
  the strict JSONL interface and direct CLI.
- Publish complete delivery directories through `tmp`, `new`, `processing`,
  `done`, and `failed` states on one filesystem.
- Expire bounded claims and redeliver unacknowledged deliveries.
- Rebuild derived queue state from project history without changing history.
- Support `addressed` awareness mode only; preserve full history queries.
- Extend bounded status and structured logs for delivery state.

## Proof

The runnable dogfood activation test from `SPEC.md` §14 shall:

1. Start two isolated session sidecars.
2. Stop the recipient before the sender publishes a signed direct message.
3. Restart and reconnect the recipient.
4. Wait for and claim the delivery through the public interface.
5. Let one claim expire and observe one redelivery.
6. Acknowledge the redelivery and observe it in `done` after restart.
7. Delete the derived queue and cursor, then restart and observe safe redelivery.
8. Verify that rebuilding did not change either durable project history.

The full Sprint S01 and S02 suites remain green. Successful evidence permits
shadow-mode dogfooding only after separate human approval.

## Not included

Broadcast and `all` awareness modes, observed traffic, artifacts, presence,
work-status events, project control changes, installation automation, and
harness-specific bridges.

## Plan adjustments

None. Update this section after each earlier sprint clearance.

## Clearance

Not cleared.

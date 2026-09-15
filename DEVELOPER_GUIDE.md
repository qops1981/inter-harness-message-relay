# Developer Integration Guide

Status: Draft for the pre-implementation protocol. Exact commands and wire
schemas are pending interface approval.

This guide describes how a harness-specific bridge integrates with the
Inter-Harness Message Relay core. `SPEC.md` is normative when this guide and the
protocol disagree. Canonical domain terms are in `CONTEXT.md`. Architectural
rationale is in `DESIGN.md` and `DECISIONS.md`.

## 1. Integration boundary

The core owns:

- authenticated replication and offline reconciliation;
- signed project events and content-addressed artifacts;
- per-session queues, cursors, awareness, and wake policy;
- work-status events; and
- TTL-bound presence state.

A harness bridge owns only:

- mapping the harness's session identity to relay configuration;
- starting and stopping one core sidecar for that session;
- translating native tool calls to the core strict JSONL interface;
- presenting messages and artifacts in the harness; and
- renewing the session lease without invoking the model.

A bridge must not reimplement persistence, replication, signing, validation,
retry state, or project lifecycle rules. The core must also remain usable from
its direct CLI without a bridge.

## 2. Session lifecycle

For each harness session, the bridge must:

1. Obtain or resume a validated `harness_id` and `session_id`.
2. Load the participant identity assigned to that session. Concurrent sessions
   must use different participant signing identities.
3. Start a dedicated core sidecar through the versioned strict JSONL interface.
   The sidecar must not require a shared fixed port.
4. Supply the project and the session's `addressed` or `all` awareness mode.
5. Renew the session lease from bridge/process code. A model turn must never be
   used as a heartbeat.
6. Wait for eligible messages instead of repeatedly invoking a polling tool.
7. On clean shutdown, report an explicit disconnect and stop the sidecar.

A resumed harness session reuses its `session_id`. Moving a participant identity
to another session is an explicit transfer performed only after the old lease
ends.

## 3. Message delivery

The core reports every delivered event with one derived relation:

- `direct`: addressed to this participant;
- `broadcast`: addressed to every project participant; or
- `observed`: addressed elsewhere but visible because awareness mode is `all`.

`addressed` mode files direct and broadcast traffic only. `all` mode also files
observed traffic. Observed traffic is batched into the next agent turn and does
not wake the agent by default. A bridge must preserve the relation label and
must not present an observed event as a request that requires an answer.

The bridge should use the blocking wait operation with its last cursor. A
successful wait claims one delivery for a bounded lease. The bridge must
acknowledge only after the harness accepts the delivery. If the bridge stops or
the claim expires first, the core can present the same delivery again.

If only non-blocking polling is possible, the bridge must honor the core's
`retry_after_ms`. Duplicate event IDs can occur after crash recovery; the bridge
must compare IDs and must not turn a repeated delivery into duplicate
non-idempotent work without confirmation.

## 4. Work progress

A bridge must preserve request linkage for these durable status events:

- `accepted` — work started;
- `progress` — a meaningful milestone;
- `completed` — terminal success; and
- `failed` — terminal failure.

`accepted` and `progress` can include `next_update_in_ms`. A requester should
wait that long after receipt before asking for status unless a notification or
operator action gives a reason to check sooner. The project history is
reconstructed from event references; bridges must not copy history into each
message.

## 5. Presence

A sidecar publishes signed, replaceable presence state with project ID and
epoch; participant and signing-key IDs; machine, harness, and session IDs; a per-process
`presence_instance_id`; a monotonic sequence; state; current request ID when
applicable; and `lease_ms`.

Presence is ephemeral. It never enters durable project history or an agent queue.
A bridge must distinguish:

- `online`: the lease is renewing;
- `offline`: an explicit clean disconnect was received; and
- `unknown`: one bounded grace interval elapsed after missed renewal or
  transport loss. A new presence instance is accepted only after explicit
  disconnect or that expiry, then its sequence starts a new comparison scope.

Online presence proves liveness only. Durable progress events prove work
progress. A bridge must not automatically reassign non-idempotent work when a
peer becomes unknown.

## 6. Storage and artifacts

Each session owns isolated local state under:

```text
projects/<project_id>/local/
  harnesses/<harness_id>/sessions/<session_id>/
```

The bridge treats IDs as opaque values and never constructs filesystem paths
from display names. It should ask the core for paths or content instead of
reaching into internal storage when a public operation exists.

Artifacts have a SHA-256 digest, byte size, and media type. The core checks the
size bound before hashing raw bytes and eagerly copies verified artifacts with
the project. A session can receive its own materialized artifact copy. Duplicate
files across sessions and machines are expected; bridge code must not add
coordination merely to save disk space.

## 7. Security

A bridge must:

- connect only through the core's local session interface;
- keep participant private keys and local authentication material out of model
  context, messages, logs, and error text;
- preserve sender, project, recipient, and relation provenance when rendering;
- treat message and artifact content as data that can contain untrusted
  instructions; and
- rely on the core for signature, authorization, size, path, and artifact-digest
  validation.

A VPN is optional. The core uses mutually authenticated TLS 1.3 with configured
machine-peer trust and must work over an untrusted network. TLS peer identity is
not participant event identity and does not replace project authorization.

## 8. Platform behavior

The core ships as a native Go binary for Linux, macOS, and Windows. A bridge
should use stdio and portable process controls first. Optional Linux/macOS setup
scripts use POSIX `sh`. Platform-specific service managers, sockets, and
supervisors are external conveniences, not core requirements.

## 9. Runtime observability

A bridge must preserve core correlation IDs when it reports errors or renders
status. It may capture the sidecar's JSON Lines `stderr`, but it must not merge
message or artifact content into operational logs.

The bridge obtains bounded runtime metrics through the existing `status`
operation. It must not require a metrics listener, tracing collector, or
telemetry backend. Cross-machine diagnosis follows request, event,
reconciliation, and delivery IDs in structured logs.

## 10. Compatibility and errors

The local interface is strict JSONL. Each request is one JSON object terminated
by LF. A bridge can strip one CR before LF, but it must not split on Unicode line
separators. It must bound a record before decoding it. Core protocol records use
`stdout`; structured logs use `stderr`.

The bridge initializes the connection before other requests, declares the major
versions it supports, and rejects an incompatible version with a clear local
error. Each request uses a correlation ID and receives one terminal success or
error response. The bridge preserves core error codes rather than guessing from
human-readable text.

Timeout is not cancellation. Cancellation is advisory and still ends with a
terminal response. Retry is a new explicit attempt linked to the prior event. A
bridge must not silently retry sends, work requests, destructive operations, or
identity transfer.

## 11. Reference harness seams

These seams guide separate bridge projects; they are not core dependencies:

- **Claude Code:** command hooks provide lifecycle JSON. A transient hook calls
  the already running session sidecar. `SessionEnd` is not reliable enough to
  replace the session lease.
- **Codex:** app-server JSONL exposes initialization, thread/turn/item events,
  item injection, steering, and approvals. A bridge pins and conformance-tests
  its schema instead of mirroring Codex thread storage or process APIs.
- **Grok:** xAI documents hooks and Claude Code compatibility. Reuse a Claude
  hook bridge only after the required Grok events, fields, blocking behavior,
  and timeouts pass conformance checks.
- **Pi:** an interactive bridge uses a small extension, starts its connection on
  `session_start`, closes it on `session_shutdown`, and selects `nextTurn`,
  `steer`, or `followUp` from wake policy. A headless integration can use Pi's
  existing strict JSONL RPC mode.

Other harnesses implement the same relay contract through their smallest stable
native lifecycle and message-injection seam.

## 12. Installation and onboarding

Every integration project must ship agent-readable installation instructions
and use the core's standalone installation contract. It must first detect and
reuse a compatible installed relay. It must not maintain a second installer or
silently replace an incompatible installation.

A normal integration-led setup should require no more than five explicit
operator actions:

1. Approve the source and version of the core binary.
2. Approve any privileged installation or persistence change.
3. Create or select a project and approve its local trust material.
4. Approve each initial peer identity and address.
5. Select awareness and wake policy, then approve the connectivity check.

After those approvals, the harness agent may perform mechanical download,
signature/checksum verification, configuration, sidecar startup, and health
checks through documented commands. The integration must show each command and
result, stop on verification failure, and never place private keys, credentials,
or transport secrets in model context or logs.

The core release must also ship a standalone operator guide. That guide covers
verified binary installation, project creation or join, trust and peer setup,
direct CLI startup, connectivity verification, upgrade, and removal. Installing
an integration later must discover and reuse that core installation.

Unsupported platforms, package managers, privilege models, and service managers
fall back to the standalone guide. The core does not add an installer daemon,
self-update framework, credential broker, or harness-specific setup logic.

## 13. Required before v1

This guide is not complete until the approved interface adds:

- literal record, body, artifact, batch, claim, and wait bounds;
- complete request, response, event, and error schemas;
- executable start, resume, wait, send, claim, acknowledge, history, status,
  and shutdown examples;
- exit codes and compatibility rules;
- crash and stale-lease recovery examples; and
- a runnable bridge-conformance command and expected output;
- the exact commands used by integration-led setup;
- a standalone `INSTALL.md` covering install, verify, upgrade, and removal; and
- a dogfood quickstart that performs the §14 activation check from `SPEC.md`.

Every public interface change must update this guide in the same reviewed
change. Its primary flows must also exist as runnable Go `Example` functions in
`_test.go` files. Those examples are part of `go test ./...` and provide
source-adjacent documentation through `go doc` and `pkgsite`.

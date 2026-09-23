# Developer Integration Guide

Status: The S01 signed-local-message profile below is implemented. Sections
1–13 describe the planned relay architecture and are not claims that later
phases are implemented.

This guide describes how a harness-specific bridge integrates with the
Inter-Harness Message Relay core. `SPEC.md` is normative when this guide and the
protocol disagree. Canonical domain terms are in `CONTEXT.md`. Architectural
rationale is in `DESIGN.md` and `DECISIONS.md`.

## Implemented S01 profile

S01 is a local, single-author profile of interface version 1. It implements
project create/open, signed direct messages, verified one-event history pages,
process status, shutdown, a strict JSONL stdio interface, and equivalent direct
commands.

Replication, queues, artifacts, presence, peer transport, and delivery remain
future work despite the normative architectural sections below. S01 also does
not implement reconciliation, claims, acknowledgments, waits, broadcasts, work
status, key rotation, or harness bridges. No macOS or Windows runtime evidence
is claimed here.

### Build and start

Use the repository-pinned Go toolchain:

```sh
mise exec -- go build -o ./relay ./cmd/relay
```

Every invocation requires this scope. Flags precede the command:

```text
[--root DIR] --project-id ID --harness-id ID --session-id ID
```

`ID` is respectively `p-`, `h-`, or `s-` plus exactly 32 lowercase hexadecimal
digits. The default root is `.ihr` in the process working directory. `--root`
selects another root; the relay resolves either value to an absolute path before
filesystem access.

The exact command forms are:

```text
relay [scope flags] stdio
relay [scope flags] initialize --id ID --params JSON
relay [scope flags] send       --id ID --params JSON
relay [scope flags] history    --id ID --params JSON
relay [scope flags] status     --id ID --params JSON
relay [scope flags] shutdown   --id ID --params JSON
```

A direct non-initialize command opens the scoped project and selects version 1.
Direct shutdown ends only that invocation. In stdio mode, send, history, status,
and shutdown require a successful initialize request on that connection.

### Requests and responses

Each stdio request is one JSON object followed by LF. One optional CR immediately
before LF is removed. Direct `--params` is the same exact params object without
JSONL framing. Unknown or additional fields are rejected.

Initialization has no `version` member. `create` is optional; omitting it opens
existing state:

```json
{"id":"init_1","op":"initialize","params":{"versions":[1],"create":{"participant_id":"u-11111111111111111111111111111111","recipient_id":"u-22222222222222222222222222222222","recipient_public_key":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"}}}
{"id":"open_1","op":"initialize","params":{"versions":[1]}}
```

Later request shapes are exact:

```json
{"version":1,"id":"send_1","op":"send","params":{"to":"u-22222222222222222222222222222222","body":"hello"}}
{"version":1,"id":"history_1","op":"history","params":{}}
{"version":1,"id":"history_2","op":"history","params":{"after_seq":1}}
{"version":1,"id":"status_1","op":"status","params":{}}
{"version":1,"id":"shutdown_1","op":"shutdown","params":{}}
```

Every complete request gets one terminal response with the same ID. The exact
outer shapes are:

```json
{"version":1,"id":"REQUEST_ID","ok":true,"result":{}}
{"version":1,"id":"REQUEST_ID","ok":false,"error":{"code":"CODE","message":"FIXED MESSAGE"}}
```

An unsafe or unavailable request ID is `null`. Results have these exact fields:

| Operation | Result |
| --- | --- |
| `initialize` | `project_id`, `project_epoch`, `participant_id`, `public_key`, `signing_key_id`, `recipient_id`, `capabilities` |
| `send` | `event_id` |
| `history` | `events`, `next_after_seq`; each item has `event_id`, `envelope` |
| `status` | `started_at_ms`, `uptime_ms`, `sent`, `rejected`, `last_error` |
| `shutdown` | Empty object |

Initialization selects project epoch 1 and returns capabilities in this order:
`send`, `history`, `status`, `shutdown`. History returns the lowest local author
sequence above `after_seq`, or above 0 when omitted. It returns zero or one
event. An empty page retains the supplied `after_seq` as `next_after_seq`.
Status counters are process-local, reset on restart, and saturate at the maximum
protocol integer. `last_error` is null or an object with `code` and `message`.

### Envelope and signing

A stored envelope contains exactly `payload` and `signature`. The payload
contains exactly `version`, `project_id`, `project_epoch`, `type`, `author`,
`signing_algorithm`, `signing_key_id`, `author_seq`, `prev`, `created`, `to`,
and `body`. `created` contains `physical_ms` and `logical`. S01 fixes version and
epoch to 1, type to `message`, and algorithm to `ed25519`.

The relay canonicalizes the payload with RFC 8785, then signs the bytes
`IHR-EVENT-V1`, one zero byte, and the canonical payload. The signature is
canonical unpadded base64url. The event ID is `sha256:` plus the lowercase
SHA-256 digest of the canonical signed envelope. Each send verifies the complete
stored author chain before deriving the next sequence, predecessor, and hybrid
logical clock. History also verifies the complete chain before returning data.

### Literal limits

All maxima are inclusive. Text byte counts are UTF-8 byte counts.

| Item | S01 limit |
| --- | --- |
| Request record | 1–131072 bytes before LF, including an optional final CR; reader detection limit 131073 |
| Direct CLI parameter JSON | 1–131072 bytes; no LF required |
| Protocol response | At most 131072 bytes before LF |
| Message body | 1–16384 decoded bytes; no trim or normalization |
| Stored envelope | 1–131072 canonical JSON bytes; no LF or trailing bytes |
| `project.json` | At most 4096 bytes |
| Private seed | Exactly 32 raw bytes |
| JSON nesting | At most 8 open containers, including the root object |
| Object members | At most 16 per object |
| Version offer | 1–8 distinct positive integers and must include 1 |
| Protocol integer | 0–9007199254740991, unsigned decimal JSON token only |
| Project epoch | Exactly 1 |
| Author sequence | 1–9007199254740991; 16 decimal digits in a final filename |
| HLC physical/logical value | 0–9007199254740991 each |
| History page | Zero or one event; `after_seq` defaults to 0 |
| Request ID | 1–64 ASCII characters in `[A-Za-z0-9_-]+` |
| Project ID | `p-` plus exactly 32 lowercase hexadecimal digits |
| Participant ID | `u-` plus exactly 32 lowercase hexadecimal digits |
| Harness/session ID | `h-` or `s-` plus exactly 32 lowercase hexadecimal digits |
| Digest | `sha256:` plus exactly 64 lowercase hexadecimal digits |
| Public key/signature | Canonical unpadded base64url, 43/86 characters |
| Status response | At most 4096 bytes |
| Log record | At most 2048 bytes before LF |
| Counter/uptime | Saturates at 9007199254740991 |
| Concurrency | One operation; no internal request queue |

S01 defines no artifact, batch, wait, claim, peer, presence, or queue limit
because those operations are unavailable.

### Stable errors, exits, and streams

| Code | Fixed message |
| --- | --- |
| `invalid_request` | `Invalid request.` |
| `record_too_large` | `Record exceeds limit.` |
| `truncated_record` | `Record lacks LF.` |
| `unsupported_version` | `Version 1 is required.` |
| `not_initialized` | `Initialize first.` |
| `already_initialized` | `Connection is initialized.` |
| `unsupported_operation` | `Operation is unavailable.` |
| `project_exists` | `Project already exists.` |
| `project_missing` | `Project does not exist.` |
| `invalid_project` | `Project state is invalid.` |
| `invalid_recipient` | `Recipient is not registered.` |
| `body_limit` | `Body length is invalid.` |
| `integer_limit` | `Event integer exceeds limit.` |
| `invalid_event` | `Stored event is invalid.` |
| `storage_error` | `Storage operation failed.` |
| `canceled` | `Operation was canceled.` |
| `internal_error` | `Operation failed.` |

Exit 0 means success or clean stdio EOF. Exit 2 means a framing, request,
protocol, trust, event-validation, integer, or cancellation failure. Exit 1
means storage, output, or unexpected internal failure. A complete recoverable
stdio request error produces one error response and the connection continues;
an oversized or truncated record produces one error response and exit 2.

Stdout contains protocol JSONL only. Stderr contains bounded JSONL operational
logs only. Logs do not contain bodies, raw requests, envelopes, keys,
signatures, seeds, paths, file contents, credentials, or raw error text. Stderr
failure is best effort. A response write failure exits 1 and never rolls back an
already published event.

### Storage and durability

All durable and temporary state is below `ROOT`:

```text
ROOT/projects/PROJECT_ID/
  project.json
  events/PARTICIPANT_ID/SEQUENCE-EVENT_DIGEST_HEX.json
  local/harnesses/HARNESS_ID/sessions/SESSION_ID/identity.seed
```

S01 creates no artifact, queue, cursor, lease, peer, archive, or log path. On
POSIX systems directories are mode 0700 and files are mode 0600. Managed files
must be regular files, managed paths must not contain symlinks, and `os.Root`
constrains relative access. The operator must prevent another same-account
process from replacing managed mounts or paths concurrently. Git ignore rules
are not a security boundary.

Create publishes `project.json` last. Event and setup publication creates an
exclusive 0600 `.tmp-` file beside the final file, writes it completely, calls
`File.Sync`, closes it, and creates the final name with a no-replace hard link.
The hard link is the acceptance point; there is no direct-write or rename
fallback. An identical existing final file is accepted without replacement. A
different or non-regular final file is rejected. Exact temporary names are
ignored by scans, and a cleanup failure after acceptance keeps success and emits
a sanitized recovery warning.

This is a process-crash guarantee on a validated local filesystem. It is not a
power-loss guarantee: directories are not synced. Kernel failure, hardware-cache
loss, NFS, SMB, and unvalidated filesystems are outside the guarantee. Native
publication behavior still needs platform-specific validation.

### Runnable create, send, restart, history, and tamper check

This script prints only stable outcomes. Each direct command is a new process,
so the history command verifies restart/open behavior. Operational logs are
captured under the temporary directory and removed by the trap.

```sh
set -eu
mise exec -- go build -o ./relay ./cmd/relay
tmp=$(mktemp -d)
trap 'rm -rf "$tmp" ./relay' EXIT
root=$tmp/state
project=p-00000000000000000000000000000001
participant=u-11111111111111111111111111111111
recipient=u-22222222222222222222222222222222
harness=h-33333333333333333333333333333333
session=s-44444444444444444444444444444444
recipient_key=A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg
scope="--root $root --project-id $project --harness-id $harness --session-id $session"

init=$(./relay $scope initialize --id init_1 --params \
  "{\"versions\":[1],\"create\":{\"participant_id\":\"$participant\",\"recipient_id\":\"$recipient\",\"recipient_public_key\":\"$recipient_key\"}}" \
  2>"$tmp/init.log")
printf '%s\n' "$init" | grep -Fq '"ok":true'
printf '%s\n' "$init" | grep -Fq '"capabilities":["send","history","status","shutdown"]'
printf '%s\n' 'initialize success; capabilities 4'

sent=$(./relay $scope send --id send_1 \
  --params "{\"to\":\"$recipient\",\"body\":\"hello\"}" 2>"$tmp/send.log")
event_id=$(printf '%s\n' "$sent" | sed -n 's/.*"event_id":"\([^"]*\)".*/\1/p')
printf '%s\n' "$event_id" | grep -Eq '^sha256:[0-9a-f]{64}$'
printf '%s\n' 'send success; valid event ID'

history=$(./relay $scope history --id history_1 --params '{}' 2>"$tmp/history.log")
printf '%s\n' "$history" | grep -Fq "\"event_id\":\"$event_id\""
printf '%s\n' 'restart history returned the same event ID'

event_file=$root/projects/$project/events/$participant/0000000000000001-${event_id#sha256:}.json
printf ' ' >>"$event_file"
if tamper=$(./relay $scope history --id history_2 --params '{}' 2>"$tmp/tamper.log"); then
  exit 1
else
  test "$?" -eq 2
fi
printf '%s\n' "$tamper" | grep -Fq '"error":{"code":"invalid_event","message":"Stored event is invalid."}'
printf '%s\n' 'tamper detected: invalid_event'
```

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

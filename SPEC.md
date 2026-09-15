# Cross-Machine Agent Messaging — Technical Specification

Status: Draft, pre-implementation. Companion to `DESIGN.md` (architecture and
rationale) and `DECISIONS.md` (alternatives considered and rejected).

## 1. Identity and Authorization

- Each machine has a stable `machine_id` and a transport identity trusted by
  configured peers. The transport identity is distinct from event authorship.
- Each harness installation has a `harness_id`, and each resumable execution
  context has a `session_id`. These identifiers are unique within their parent
  scope.
- Each participant has a stable opaque participant ID and one active Ed25519
  signing key for events and presence signals. The key ID is
  `sha256:<lowercase hex>` over the raw 32-byte
  public key; public keys use unpadded base64url when serialized. Project trust
  maps the participant ID to its active key.
- A participant maps to one session at a time. Concurrent sessions use different
  participant IDs and keys, so only one writer can advance an author chain.
- Each project has exactly one project administrator in v1. This avoids
  conflicting offline control histories. The administrator key, initial
  participant keys, project ID, project epoch, and trusted transport peers are
  installed out of band when the project is created.
- The operator must keep an offline recovery copy of the administrator key.
  Losing every copy blocks membership and lifecycle changes but does not make
  accepted project history or ordinary current-epoch messaging unreadable.
- Only the project administrator can issue control events that add or revoke a
  participant, replace a lost key, rotate the administrator key, close the
  project, or reopen the project. Each accepted control event advances the
  project epoch by one.
- Participant key replacement is an administrator control event. The active
  administrator rotates its own key with `admin_rotate` signed by the old key.
  Events signed by a revoked key remain verifiable history but cannot authorize
  later events.
- A display name is metadata. No display name identifies a machine, harness,
  session, participant, project, transport peer, or filesystem path.

## 2. Event Format and Ordering

Project history is a set of immutable signed event envelopes. Each participant
owns one gap-free author chain.

```json
{
  "payload": {
    "version": 1,
    "project_id": "<validated opaque ID>",
    "project_epoch": 7,
    "type": "message",
    "author": "<participant ID>",
    "signing_algorithm": "ed25519",
    "signing_key_id": "<participant key ID>",
    "author_seq": 42,
    "prev": "sha256:<previous event digest>",
    "created": {"physical_ms": 1789400000000, "logical": 0},
    "to": "<participant ID or broadcast>",
    "in_reply_to": "<event ID or null>",
    "request_id": "<caller request ID or null>",
    "body": "<bounded inline text or null>",
    "artifact": {
      "digest": "sha256:<lowercase hex>",
      "size": 1234,
      "media_type": "text/plain"
    }
  },
  "signature": "<unpadded base64url signature>"
}
```

- Event types are `message`, `accepted`, `progress`, `completed`, `failed`,
  and the control types defined in §8. A message has a body, an artifact, or
  both. The literal string `broadcast` in `to` addresses all project
  participants; every other recipient is a participant ID.
- Fields that do not apply to an event type are absent, not `null`. `prev` is
  the one exception: it is present and JSON `null` when `author_seq` is one.
  The interface PPP must define the required and permitted fields for every
  event type before implementation.
- The unsigned payload is canonicalized with RFC 8785. The author signs
  `IHR-EVENT-V1`, one zero byte, and the canonical payload bytes. The event ID
  is `sha256:<lowercase hex>` over the canonical signed envelope.
- JSON objects with duplicate keys, invalid Unicode, floats, unsafe integers,
  unknown fields, or an unsupported version are rejected. Protocol constants
  bound inline body bytes, envelope bytes, and artifact bytes before allocation;
  the interface PPP must select the literal v1 limits before implementation.
- `author_seq` starts at one. `prev` names the preceding event ID. A stored
  `{author, author_seq}` with a different event ID is a fork and is quarantined
  and reported. An identical event is a harmless duplicate.
- Each author updates its hybrid logical clock when it creates an event and when
  it accepts an event. `(physical_ms, logical, author, author_seq, event_id)`
  gives replicas with the same event set the same display order. This order is
  a derived view, not a replication cursor or promise that late offline events
  never appear earlier in history.
- `in_reply_to` and `request_id` express conversation and work causality. Display
  order never replaces those explicit links.

## 3. Reconciliation (Layer 0)

- Each machine keeps a full usable copy of durable events and artifacts for each
  active or locally retained project. Project history is a set of per-author
  chains, not one physically ordered multi-writer log.
- Peers exchange a reconciliation cursor that maps each participant ID to the
  highest contiguous accepted `author_seq`. A peer requests missing ranges
  per author. One global event ID or timestamp is never a reconciliation cursor.
- A receiver can stage events that arrive before an author-chain gap, but it
  advances an author cursor only after the complete preceding chain validates.
  It requests the gap from any authenticated project peer that advertises the
  range. An event from a future unknown project epoch also remains staged until
  the administrator control chain through that epoch validates.
- Before publication, the receiver validates the envelope and limits, event ID,
  signature, author trust at the stated project epoch, previous hash, sequence,
  project ID, event type, recipient membership or the `broadcast` constant,
  artifact descriptor, and control-event authorization.
- After a replica learns a newer project epoch, a valid older-epoch event remains
  in history as `stale` but is not filed, executed, or allowed to change current
  state. A partitioned replica can deliver the event before learning the newer
  epoch; close and revocation are not global barriers without consensus. The
  author must publish a new current-epoch event when the intent still applies.
- Reconciliation is bidirectional and repeats after reconnect. A closed project
  continues low-rate reconciliation so replicas can learn an authorized reopen;
  closing stops ordinary writes and session filing, not control discovery.
- The no-loss goal assumes at least one correct retained copy, eventual contact
  with that copy, retention until replication completes, and successful
  validation. Signatures cannot recover data after every copy is lost.

## 4. Artifact Store

- An artifact descriptor contains `digest`, `size`, and `media_type`. V1 accepts
  only `sha256:<64 lowercase hex characters>` digests.
- The digest covers the raw artifact bytes. Filenames, timestamps, permissions,
  compression, and other filesystem metadata do not affect artifact identity.
- A receiver rejects a transfer that exceeds the declared size or the protocol
  size limit before hashing. It then verifies the exact byte count and SHA-256
  digest before publication.
- Artifacts are eagerly copied to every participating machine and can be fetched
  from any authenticated holder. A source participant can be recorded as event
  provenance but is never the only availability location.
- Each interested session can copy the verified bytes into its delivery
  directory as `artifact`. Duplicate copies across sessions and machines are
  expected and acceptable.
- V1 uses whole-artifact transfer and explicit project purge. Chunking,
  compression, algorithm agility, automatic garbage collection, and global
  deduplication are deferred until a measured limit requires them.

## 5. Local Filesystem Layout (Layer 1 — Session Delivery)

```
projects/<project_id>/
  events/<participant_id>/<zero-padded-sequence>-<event-digest-hex>.json
  artifacts/tmp/<transfer-id>
  artifacts/sha256/<artifact-digest-hex>
  local/
    harnesses/<harness_id>/
      sessions/<session_id>/
        queue/
          tmp/<delivery_id>/
          new/<delivery_id>/
          processing/<delivery_id>/
          done/<delivery_id>/
          failed/<delivery_id>/
        cursor.json
        config.json
  archived
```

- Incoming artifact bytes are first written under `artifacts/tmp` and are moved
  to the digest path only after size and hash validation.
- The sidecar constructs a complete delivery directory in `tmp`, including
  `meta.json` and any materialized artifact. It flushes the required files,
  validates the directory, and publishes it to `new` without exposing partial
  contents.
- Staging and final paths must use one filesystem. Go does not promise atomic
  rename on every non-Unix platform, so the implementation PPP must define and
  test the publication primitive and durability assumptions for Linux, macOS,
  and Windows. NFS and SMB locking or rename behavior is not assumed safe.
- One sidecar owns one session queue. No file lock or competing-consumer protocol
  is required.
- Claim moves one delivery from `new` to `processing` and records a bounded claim
  lease. On restart or claim expiry, the sidecar returns the delivery to `new`.
  Duplicate presentation is therefore possible.
- Acknowledgment moves `processing` to `done`. Acknowledgment means the harness
  accepted the delivery; it does not mean requested work completed. A permanent
  presentation error moves the delivery to `failed`; retry is explicit.
- Project history remains authoritative. Queue directories and filing cursors
  are local derived state. If local delivery state is lost, rebuilding can
  safely redeliver events; it must never delete or mutate project history.
- `harness_id`, `session_id`, `project_id`, participant IDs, event digests, and
  delivery IDs are generated or strictly validated opaque path segments.
  Display names and raw harness values are never paths.
- `addressed` mode files direct and broadcast events. `all` mode also files
  non-addressed events as `observed`. Observed events do not imply a request and
  do not wake the model by default.
- The sidecar advances its filing cursor past filtered events. An awareness-mode
  change affects future filing; older history stays queryable without automatic
  context backfill.

## 6. Local Adapter Process — Responsibilities

One instance of the adapter daemon runs per harness session. Every instance uses
the same harness-agnostic executable, owns exactly one participant identity,
`harness_id`, `session_id`, queue, cursor, configuration, and lease, and talks to
its caller through the versioned strict JSONL interface. It must not bind a shared
fixed port. All requests are implicitly scoped to that instance:

**Write path:**
1. The harness sends an event type, recipient, bounded body, optional artifact,
   request ID, and optional reply event ID.
2. For an artifact, the sidecar computes the descriptor, stages the bytes,
   verifies size and digest, and publishes the artifact.
3. The sidecar allocates the next author sequence, names the previous event,
   creates the current-epoch payload, canonicalizes and signs it, and publishes
   the immutable envelope.
4. Reconciliation advertises the new author sequence and artifact (§§3–4).

**Read path:**
1. The sidecar scans accepted project events from its filing cursor.
2. The sidecar derives `direct`, `broadcast`, or `observed` for each eligible
   event and stages the complete delivery under its session queue.
3. `wait` or `poll` claims a ready delivery for bounded presentation to the
   harness. The caller can request materialized artifact bytes or only the
   verified descriptor.
4. Explicit acknowledgment completes the delivery. An expired claim returns to
   ready state and can cause duplicate presentation.

## 7. Notification, Progress, and Liveness

### 7.1 Agent polling

- The adapter monitors replicated state without invoking an agent or consuming
  model tokens.
- Harness adapters should use `wait(after_cursor, timeout)`, which blocks until
  an eligible message exists or the timeout expires.
- If a harness cannot block or receive notifications, `poll(after_cursor)` must
  return `retry_after_ms`. The adapter enforces configurable minimum and maximum
  poll intervals and may add jitter, so a neurotic caller cannot busy-loop.
- Direct messages and broadcasts may wake an agent according to local harness
  policy. `observed` messages are batched and do not wake an agent by default.

### 7.2 Work status

A request may receive durable events linked by `in_reply_to`:

- `accepted`: work started; may include `next_update_in_ms`;
- `progress`: a meaningful milestone or revised `next_update_in_ms`;
- `completed`: terminal success with result or artifact descriptor;
- `failed`: terminal failure with a short reason.

A requester should not perform a status check before `next_update_in_ms` has
elapsed since receipt unless it receives a notification or has an explicit
reason to intervene. The adapter can
compute response-time statistics from existing events; messages must not embed
copies of conversation or timing history.

### 7.3 Presence and heartbeat

- Each session adapter daemon monitors its one harness process. The harness
  session maintains a renewable lease through its process connection; renewal
  never invokes the model.
- Adapters exchange signed `presence` signals containing project ID and epoch,
  participant ID, signing key ID, `machine_id`, `harness_id`, `session_id`, a
  random `presence_instance_id` created at sidecar start, a monotonically increasing
  presence sequence, state (`idle` or `busy`), current request ID if any, and
  `lease_ms`.
- Presence uses RFC 8785 canonical JSON and signs `IHR-PRESENCE-V1`, one zero
  byte, and the canonical signal bytes. Presence signals travel over the
  existing authenticated TLS peer connections.
- A presence signal is replaceable ephemeral state, not a durable event. For one
  presence instance, the receiver accepts only a newer sequence and enforces a
  maximum `lease_ms`. It accepts a different instance only after explicit clean
  disconnect or expiry of the prior lease and grace interval. It then resets the
  expected sequence. This avoids synchronized wall clocks and stale-instance
  rollback.
- Remote status is `online` while the lease renews and `offline` after an
  explicit clean disconnect. After missed renewal or transport loss, the
  receiver keeps the last state during one bounded grace interval and then
  reports `unknown`. The grace interval does not extend authorization.
- `online` proves only that the adapter/session lease is renewing. It does not
  prove that work is progressing; durable work-status events provide that
  evidence.
- A lost lease must notify interested local agents, but must not automatically
  retry or reassign non-idempotent work.

### 7.4 Retention classes

- `durable` events are signed project messages, work-status events, and control
  events. They remain in replicated history until explicit project purge.
- `ephemeral` signals are presence/heartbeat or similar replaceable monitoring
  state. They have a bounded lease/TTL, are best-effort, and never enter the
  project history or session queue.
- Correctness, authorization, work requests, results, and lifecycle changes
  must never depend only on an ephemeral signal. Arbitrary intermediate
  persistence levels are out of scope for v1.

## 8. Project Control and Lifecycle

- V1 control event types are `member_add`, `member_revoke`, `key_replace`,
  `admin_rotate`, `close`, and `reopen`. Only the active project administrator
  can author them.
- Each control event names the current project epoch and establishes exactly the
  next epoch. A skipped, repeated, stale, or unauthorized control event is
  quarantined for diagnostics and cannot enter project history or change state.
- `member_revoke` and `key_replace` affect later epochs. Historical signatures
  remain verifiable under the trust state of their own epoch.
- `close` advances the epoch and marks the project archived. The sidecar writes
  an empty derived `archived` marker after accepting `close` and removes the
  marker after accepting `reopen`. The marker is not authoritative.
- An archived project rejects ordinary new events and stops session filing, but
  continues bounded
  reconciliation so it can receive control history.
- `reopen` advances the epoch and makes the project active. Only the current
  administrator can reopen it; a local marker edit is not a reopen operation.
- Regular events from a prior epoch remain visible as stale history but are not
  delivered or executed. A participant republishes the intent in the current
  epoch when it remains valid.
- Purge is a separate explicit local operation. V1 has no age-based automatic
  purge or artifact garbage collection. Purge must require local human
  confirmation and never propagates as an ordinary event.

## 9. Transport Security

- V1 uses TLS 1.3 with mutual authentication from Go's standard library.
  Project setup installs trusted certificate authorities or exact peer
  certificate/public-key identities out of band; a public CA is not required.
- Transport identity is a machine/peer property and is distinct from participant
  event-signing identity. A peer is accepted for a project only when both the
  TLS identity and project trust configuration authorize it.
- The system must work across an untrusted network without requiring a VPN.
  A trusted LAN, WireGuard, Tailscale, or another private mesh is optional
  routing and defense in depth.
- TLS supplies connection confidentiality, integrity, and peer authentication.
  The application still validates project membership, epoch freshness, event
  signatures and chains, duplicate or replayed events, and artifact hashes.
- V1 uses configured peer addresses and bounded reconnect backoff with jitter.
  Global discovery, ICE, STUN, TURN, and a mesh-routing framework are out of
  scope.

## 10. Explicitly Out of Scope for v1

- Byzantine-fault-tolerant consensus of any kind.
- Global peer discovery / mesh membership protocols beyond a static trusted
  key list per project.
- Chunked/resumable artifact transfer (only relevant if artifacts grow to
  the point that whole-file transfer over the existing link becomes
  impractical — not needed at currently anticipated sizes, e.g. code
  reviews/diffs).
- End-to-end payload encryption independent of the required encrypted
  machine-to-machine transport.
- Agent scheduling, orchestration, workflow execution, or task execution.
- Starting, supervising, or replacing harness processes or operating-system
  service managers.
- A plugin runtime, dashboard platform, general database, general message bus,
  or arbitrary key-value store.
- Pi extensions, Claude Code hooks, Codex adapters, Grok plugins, or other
  harness-specific bridges.
  Those are separate projects that call the core's harness-neutral interface.

## 11. Implementation and Platform Requirements

- The core adapter daemon is implemented in Go and shipped as a native binary
  for Linux, macOS, and Windows.
- Prefer the Go standard library. Add a library only where implementing a
  security or interoperability standard locally would be riskier (for example,
  RFC 8785 canonicalization).
- Keep the core build pure Go where practical so cross-compilation does not
  require a platform C toolchain.
- Core behavior must not depend on Bash, Ruby, Unix signals, `flock`, `inotify`,
  launchd, systemd, or another platform-specific facility. Use Go filesystem,
  process, networking, TLS, and cryptography interfaces.
- The core exposes a versioned, harness-neutral strict JSONL interface on
  stdio and a direct CLI. Each protocol record is one JSON object terminated by
  LF; input may contain CRLF by stripping one trailing CR. A reader must not
  treat Unicode line separators as record boundaries.
- The interface bounds a record before JSON decoding. `stdout` contains protocol
  records only, and `stderr` contains structured logs only.
- The first request initializes the interface version and capabilities. Each
  later request has a correlation ID and receives one terminal success or error
  response. Cancellation is advisory and still produces a terminal response.
- The core must not import or depend on a harness SDK.
- Pi extensions, Claude Code hooks, Codex adapters, Grok plugins, MCP wrappers,
  and other
  harness-specific bridges live in separate projects. They translate native
  harness calls only; replication, persistence, signing, and validation remain
  in the core.
- Platform-specific sockets and process supervisors may be optional external
  bridges, not core requirements.
- Optional Linux/macOS setup helpers must use POSIX `sh`, not newer Bash-only
  features. Windows users run the native executable directly; a PowerShell
  helper is added only if installation actually needs one.
- Release checks must cover Linux and macOS plus at least one supported Windows
  build. Filesystem tests must exercise the documented same-filesystem
  publication, flush, recovery, and path behavior on each supported operating
  system without assuming identical rename guarantees.
- Every public Go package and exported identifier must have an idiomatic Go doc
  comment. Public integration flows must have runnable `Example` functions in
  `_test.go` files so the documentation is also tested by `go test` and can be
  rendered by `go doc` or `pkgsite`.
- Documentation comments explain contracts, invariants, errors, and non-obvious
  behavior. Internal implementation receives comments only where the reason is
  not clear from the code.
- Use a bounded set of long-lived goroutines. Do not create a goroutine per
  message or use channels as an internal message bus.
- Use `context.Context` for cancellation and deadlines. Prefer one-goroutine
  state ownership; use `sync.WaitGroup`, mutexes, and `sync/atomic` only when
  ownership cannot remain local.
- Use monotonic elapsed time for session leases and relative update intervals.
- Use `crypto/ed25519`, `crypto/sha256`, `crypto/rand`, `crypto/tls`, and
  `crypto/x509` for the corresponding protocol responsibilities.
- Decode untrusted input through bounded readers into typed structs. Validate
  explicit protocol versions and reject fields outside the accepted schema.
  Use distinct Go types for project, participant, harness, session, and event
  identifiers.
- Use wrapped errors with `errors.Is`/`errors.As` support for stable error
  classification. Callers must not depend on parsing error text.
- Use `io/fs`, `os`, and `path/filepath` for portable storage. Publish immutable
  data using exclusive creation or a synced same-filesystem temporary object
  followed by the platform-verified publication operation. Validate every opaque
  path segment.
- Use `log/slog` with a JSON handler for the logging requirements in §15.
- Tests should use `t.TempDir`, `net.Pipe`, `httptest`, runnable examples, native
  fuzzing, and `go test -race ./...` where they match a behavior. Normal checks
  include `gofmt`, `go vet ./...`, and `go test ./...`.
- The core must not use `unsafe`, Go plugins, reflection-heavy dependency
  injection, unbounded goroutines, or CGO without a separately approved
  requirement. Generics and build tags require a concrete repeated or
  platform-specific need.
- Use `github.com/gowebpki/jcs` for RFC 8785 canonicalization. Keep it behind one
  internal wrapper and run local RFC-vector regression tests so the dependency
  can be replaced without changing callers.
- Use `golang.org/x/sync/errgroup` only when the approved lifecycle contains
  multiple error-returning goroutines that require shared cancellation and
  error propagation. Select a release compatible with the project's minimum Go
  version.
- Use standard `testing/synctest` for lease, timeout, retry, and heartbeat tests
  when the selected minimum Go version supports it.
- Do not add `fsnotify` initially. Reconciliation scans remain authoritative;
  add filesystem notifications only after measured scan cost or delivery
  latency justifies a second path.
- Do not import Syncthing packages. If Syncthing is selected as a replication
  implementation, treat its executable as an independently operated external
  tool.
- Run the official `govulncheck` command during dependency verification and
  release checks. It is a development tool, not a product import.

## 12. Developer Integration Guide

`DEVELOPER_GUIDE.md` is a required versioned release artifact. Before v1 it must
contain:

- the supported core-interface version and compatibility policy;
- strict JSONL framing plus complete request, response, event, and error schemas;
- session start, resume, lease renewal, wait, shutdown, and recovery sequences;
- identity, addressing, awareness, wake, progress, presence, retention, and
  artifact rules;
- security requirements and trust-boundary guidance;
- runnable direct-CLI and bridge examples;
- a conformance checklist with commands a bridge developer can run; and
- the agent-readable integration onboarding contract, including no more than five
  explicit operator approval actions for a normal supported setup.

`INSTALL.md` is also a required versioned release artifact. It provides the
standalone core-first installation flow for operators who install the relay
before any harness integration. Exact runnable commands are added when the
release artifacts and public CLI exist.

Any change to the public interface or integration behavior must update the guide
in the same reviewed change. The guide explains integration; `SPEC.md` remains
the normative protocol source.

## 13. Implementation Workflow

- Every nontrivial implementation must run through Forge.
- PPP must produce numbered EARS-lite behaviors, plain-English pseudocode,
  decisions, and an expected file surface. Implementation must not start until
  a human approves that feature spec.
- TDD must implement one approved behavior at a time using RED, minimal GREEN,
  and REFACTOR. The behavior ID must appear in its test name.
- A RED test is frozen after it fails for the expected reason. Later changes
  require an explicit spec-change flag and renewed approval where behavior
  changes.
- The full suite must run after each GREEN. Final verification must compare
  failures with the recorded baseline and check behavior coverage, public Go
  examples, developer-guide fidelity, and the approved file surface.
- Trivial changes run the PPP minimalism gate inline. Observable behavior
  changes still require a failing check before the fix. Pure documentation
  changes require review but no artificial test.
- Forge preflight must verify the Git repository, dedicated worktree, panels,
  referenced assets, language commands, and enforcement hooks. A missing
  prerequisite blocks a claimed Forge run unless a human explicitly approves a
  documented manual-gate variant.

## 14. Dogfooding

Dogfooding starts when one runnable acceptance check proves that two isolated
session sidecars can exchange and acknowledge a signed direct message through
the public core interface. The check must also prove that an offline recipient
receives the queued message after restart or reconnect.

Initial dogfooding rules:

- Use the relay for real coordination on this project, starting with low-risk
  messages and the direct CLI or first available external bridge.
- Run in shadow mode with an independent fallback communication channel. A
  relay failure must not prevent recovery or human intervention.
- Record delivery IDs and failures outside the relay when diagnosing a relay
  outage.
- Convert every reproducible dogfood defect into a failing TDD regression check
  before applying its fix.
- Dogfood evidence supplements automated tests; it never replaces them.
- Move to relay-only project coordination only after a human reviews successful
  same-machine, cross-machine, offline-recovery, duplicate-delivery, and
  sidecar-restart evidence and explicitly approves removal of the fallback.

## 15. Runtime Observability

### 15.1 Structured logging

- Each sidecar must write one JSON object per line to `stderr` using Go's
  standard structured logging facilities.
- Levels are `debug`, `info`, `warn`, and `error`. Normal lifecycle transitions
  use `info`; per-event diagnostic detail uses `debug`; recoverable abnormal
  state uses `warn`; failed requested operations use `error`.
- Logs must cover startup/shutdown, lease transitions, peer connection and
  authentication, event acceptance/rejection, delivery/claim/acknowledgment,
  reconciliation, signature/artifact validation failures, queue recovery, and
  unexpected filesystem state.
- Entries include applicable project, participant, machine, harness, session,
  request, event, peer, reconciliation, and delivery IDs. Existing protocol IDs
  provide correlation; logging must not invent a second distributed trace ID.
- Logs must never contain message bodies, artifact content, private keys,
  credentials, transport secrets, or authentication material. Error fields must
  be sanitized before logging.
- The sidecar must not manage log files, rotation, shipping, or retention. Its
  caller may redirect or collect `stderr`.

### 15.2 Status metrics

- The existing `status` CLI/stdio operation returns a bounded JSON snapshot. The
  sidecar must not add an HTTP listener, Prometheus endpoint, metrics database,
  exporter, or dashboard.
- The snapshot includes process start/uptime, queue depth by state, cumulative
  message outcomes, peer and presence state, reconciliation outcomes, pending
  event/artifact counts, bytes transferred, lease freshness, last successful
  reconciliation, and the most recent sanitized operational error.
- In-process counters may reset on sidecar restart; the snapshot must expose the
  process start time so callers can detect that reset.
- Cumulative counters must have bounded cardinality and must not use message,
  event, or participant IDs as dimensions. The bounded peer/presence roster may
  identify its configured participants.

### 15.3 Tracing

- v1 has no dedicated distributed-tracing framework, span exporter, collector,
  or OpenTelemetry dependency.
- Operators trace an operation through correlated structured logs using request,
  event, reconciliation, and delivery IDs.
- Dedicated tracing may be proposed only with recorded evidence that correlated
  logs cannot diagnose a real cross-machine failure.

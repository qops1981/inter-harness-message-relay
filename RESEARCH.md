# Inter-Harness Message Relay — Independent Research Review

Date: 2026-09-12; aligned with the 2026-09-14 prior-art review

## Executive conclusion

The problem is real, and the high-level split between durable project history,
local delivery state, and artifacts is sound. This review originally found
blocking defects in the draft. The 2026-09-14 alignment in `SPEC.md`,
`DESIGN.md`, and `DECISIONS.md` now corrects the global cursor, HLC order,
artifact availability, and Maildir delivery claims described below.

The smallest credible v1 is:

1. use mutually authenticated TLS 1.3 with configured peer trust and per-author
   range reconciliation; keep Syncthing as an optional independently operated
   adapter or comparison oracle rather than a required runtime;
2. keep queue/read state outside the synchronized directory;
3. model the history as **one signed, hash-chained sequence per author**, not one physically ordered multi-writer log;
4. derive a deterministic conversation view without claiming that it is a consensus order; and
5. replicate blobs eagerly in v1 so the advertised durability guarantee is true.

This delegates the hardest undifferentiated work—peer discovery, reconnects, anti-entropy, block transfer, integrity checks, and multi-source fetching—to a mature existing system. A custom gossip transport can replace it later without changing the event format if measured constraints require that.

The operating bias is storage over processing: complete replicas, duplicated per-session artifacts, and rebuildable local queues are preferred when they remove coordination, reconstruction, network fetches, or model work. Content hashes provide identity and integrity; global deduplication is not a v1 objective.

The product boundary is deliberately narrow: authenticated project message/artifact exchange, offline reconciliation, and bounded presence. Harnesses retain process lifecycle and agent execution. Scheduling, orchestration, service management, workflow engines, dashboards, plugin hosting, and general database/message-bus behavior should remain composed external tools, not core features.

A versioned `DEVELOPER_GUIDE.md` is a required release artifact. It owns the integration sequence for external and first-party bridge developers, while `SPEC.md` remains normative. Every public Go package and exported identifier also receives idiomatic Go documentation, and primary integration flows are runnable `_test.go` `Example` functions. Interface changes update code, source documentation, guide, examples, and conformance checks together.

The core should be implemented in pure Go and released as native Linux, macOS, and Windows binaries. This protocol needs robust TLS, Ed25519, concurrency, timers, and crash-safe filesystem behavior, making shell a false economy. Go provides most of that in its standard library with simpler cross-compilation than the alternatives. Use focused libraries only for standards that should not be reimplemented casually, notably RFC 8785 canonicalization. Keep POSIX `sh` for optional Linux/macOS setup helpers; do not make shell or Ruby a runtime dependency.

## What is good in the draft

- **Harness independence is the correct boundary.** The transport and durable state should not depend on a Pi or Claude plugin.
- **Project-scoped state is operationally useful.** It gives membership, retention, export, and recovery a natural boundary.
- **Immutable events are a good fit.** Close, reopen, acknowledgment, and supersession should be later events rather than mutations.
- **Pull by default is safer than unsolicited context injection.** A receiving agent should decide when to load a message or artifact.
- **Content addressing is appropriate for artifacts.** SHA-256 references provide stable identity, deduplication opportunities, and verification.
- **Per-participant signing is useful even on a private transport.** Transport identity authenticates a machine connection; an event signature preserves authorship after forwarding or storage.

## Blocking issues found in the earlier draft

The aligned normative documents now apply the resolutions below. This section
retains the original evidence and failure analysis.

### 1. “Highest ID, then everything after it” is not valid multi-writer anti-entropy

A single high-water mark works only for one gap-free sequence. It fails for independent writers or partial dissemination. For example, A can hold IDs `10` and `30`, while B holds `10`, `20`, and `30`. Exchanging `30` causes A to request nothing and miss `20` forever.

The established shape is a sequence per author/feed plus a map of the highest contiguous sequence known for each author. Secure Scuttlebutt uses an append-only feed per identity with a sequence number and previous-message hash; its replication API exchanges a vector clock keyed by feed. Syncthing similarly tracks file versions and announced indexes rather than using one global maximum.

**Required correction:** add `author_seq` and `prev` to every event. Synchronize from a map such as `{participant_id: highest_contiguous_seq}`. Request explicit missing ranges; do not infer set equality from a maximum ID.

Sources:

- Secure Scuttlebutt feed format and replication: <https://github.com/ssbc/secure-scuttlebutt>
- SSB epidemic broadcast-tree vector clock API: <https://github.com/ssbc/ssb-ebt>
- Syncthing synchronization model: <https://docs.syncthing.net/users/syncing.html>

### 2. The HLC sort tuple is backwards

`SPEC.md` orders by `(ts.logical, ts.physical, from)`. Hybrid logical timestamps compare the physical/wall component first and the logical component only when wall times match. CockroachDB's production HLC implementation compares `WallTime`, then `Logical`.

Even corrected to `(physical, logical, author)`, this creates a deterministic **view**, not a single append-only storage order: a late-arriving offline event can sort into the middle of previously displayed history. That is acceptable, but the documentation must say so.

**Required correction:** use `(physical_ms, logical, author, author_seq, event_id)` for display if HLC is retained. Update the local HLC on both local creation and receipt. Do not use this tuple as the replication cursor.

Source: CockroachDB HLC timestamp ordering: <https://github.com/cockroachdb/cockroach/blob/master/pkg/util/hlc/timestamp.go>

### 3. ULID is an identity convenience, not a replication cursor

The ULID specification says ordinary ULID order is not guaranteed within the same millisecond; its monotonic factory increments randomness for calls made by that factory in the same millisecond. It does not create a gap-free global sequence across machines.

**Required correction:** identify an event by a hash of its canonical signed payload, or retain a ULID only as a display-friendly opaque ID. Use per-author sequence numbers for replication.

Source: ULID specification: <https://github.com/ulid/spec>

### 4. The blob policy contradicts the durability goal

The original draft said every participant had a full working copy of shared state while storing a blob only at `blob_origin` until requested. If the origin disk failed first, the signed message survived but its artifact did not; `blob_origin` also failed to advertise alternate holders.

**Resolved for v1:** synchronize content-addressed blobs to every project participant. They remain outside message metadata, so they do not bloat event scanning. If artifacts later become large enough for lazy replication, define a holder set, replication factor, availability acknowledgment, retry across holders, and garbage-collection rules.

Syncthing already splits files into hashed blocks, verifies received blocks, and can obtain blocks from another device in the cluster.

Source: Syncthing block transfer and verification: <https://docs.syncthing.net/users/syncing.html#blocks>

### 5. The Maildir adaptation currently overclaims crash safety

Maildir's important write rule is: completely write a uniquely named file under `tmp`, then move it to `new`. The draft instead says the filer “materializes” a message directory directly under `new`, which can expose partial `meta.json` or artifact content.

The draft also gives two conflicting meanings to `new → cur`: one section calls it pickup; another calls it acknowledgment. If it means pickup and the agent crashes afterward, the item is stranded in `cur`. If it means completion, concurrent consumers can both begin work before either rename occurs.

**Required correction:** stage the complete directory outside `new`, fsync as required by the desired durability level, and atomically rename it into `new`. Then explicitly choose a delivery contract:

- **at-most-once attempt:** claim by renaming before injection; a crash may require manual retry;
- **at-least-once attempt:** claim with a lease and requeue stale claims; handlers must tolerate duplicate event IDs; or
- a durable harness-specific handoff that can prove injection before acknowledgment.

Exactly-once agent action is not supplied by filesystem rename.

Source: original Maildir description: <https://cr.yp.to/proto/maildir.html>

### 6. Signed JSON needs a canonical byte definition

“Sign the canonicalized entry” is not interoperable until the canonicalization scheme and accepted JSON value domain are normative. Different runtimes can serialize key order, numbers, Unicode, and escaping differently. RFC 8785 defines JSON Canonicalization Scheme (JCS), including duplicate-key rejection, IEEE-754 number constraints, recursive property sorting, and UTF-8 output.

**Required correction:** specify RFC 8785 exactly, or sign an opaque payload byte string and carry it in an envelope. JCS is the more inspectable option, but integer millisecond timestamps and sequence numbers must remain within the interoperable integer range. Specify signature and key encodings, domain separation, and protocol version.

Sources:

- RFC 8785, JSON Canonicalization Scheme: <https://www.rfc-editor.org/rfc/rfc8785>
- Libsodium public-key signature documentation: <https://doc.libsodium.org/public-key_cryptography/public-key_signatures>

### 7. The event schema is incomplete for its own lifecycle protocol

The base record has no `type`, although close is later described as `type: "close"`. It also does not define:

- protocol/schema version;
- author sequence and previous-event hash;
- key ID and key rotation/revocation;
- message, close, reopen, supersede, and acknowledgment event bodies;
- reply/causal references;
- media type and original filename for artifacts;
- whether `body` and `blob_ref` are exclusive;
- hard body/blob limits;
- unknown-field behavior;
- authorization for close, reopen, membership changes, and purge; or
- validation of `project_id` before using it in a filesystem path.

These are protocol boundaries, not optional implementation detail.

### 8. Close/reopen is underspecified under offline concurrency

A worker can create messages offline while another participant closes the project. On merge, the system must decide whether those events remain visible, are rejected, or belong to a later project epoch. “Stop gossiping when archived” can also prevent a node from learning about a subsequent reopen.

**Required correction:** keep replication active for archived metadata, or define a separate membership/control channel. Give the project a monotonically increasing epoch. State who may close/reopen it and what happens to events authored against an old epoch.

### 9. Signatures do not by themselves establish authorization or safe prompting

Ed25519 proves that a holder of a private key signed bytes. Verifiers still need an already-trusted public key, and the application must decide what that key is authorized to do. A validly signed artifact can still contain malicious instructions copied from an untrusted repository.

**Required correction:** maintain project-scoped roles/capabilities, default inbound delivery to pull/no-auto-trigger, render sender and project provenance, cap inputs before allocation, and treat artifact text as data rather than trusted instructions.

Libsodium explicitly notes that verifiers must already know and trust a public key.

Source: <https://doc.libsodium.org/public-key_cryptography/public-key_signatures>

## Recommended v1 architecture

```text
project-root/
  events/<participant-id>/
    <zero-padded-seq>-<event-digest-hex>.json
  artifacts/sha256/<artifact-digest-hex>
  local/
    harnesses/<harness-id>/
      sessions/<session-id>/
        queue/tmp/<delivery-id>/
        queue/new/<delivery-id>/
        queue/processing/<delivery-id>/
        queue/done/<delivery-id>/
        queue/failed/<delivery-id>/
        cursor.json
        config.json
```

### Replication

V1 uses mutually authenticated TLS 1.3 with configured peer addresses and
out-of-band trust. Peers exchange the highest contiguous accepted sequence per
author and request missing ranges. A receiver validates the complete preceding
chain before advancing a cursor. One global maximum cannot establish set
equality.

Only immutable uniquely named event and artifact files are published. Each file
is staged and validated on the same filesystem before the platform-specific
publication operation. Accepted events are never edited in place. Closed
projects continue bounded reconciliation so replicas can learn an authorized
reopen; purge remains a separate confirmed local operation.

Syncthing remains valuable prior art and an optional independently operated
adapter. It is not imported, started as a relay child, or required for the core
runtime.

Sources:

- Synchronization and conflict behavior: <https://docs.syncthing.net/users/syncing.html>
- TLS device identity, discovery, and relay privacy: <https://docs.syncthing.net/users/security.html>
- Ignore-file behavior: <https://docs.syncthing.net/users/ignoring.html>

### Event model

Use a per-author chain, borrowing the proven shape of SSB without adopting its aging implementation stack:

```json
{
  "payload": {
    "version": 1,
    "project_id": "opaque-safe-id",
    "project_epoch": 1,
    "type": "message",
    "author": "participant-id",
    "author_seq": 42,
    "prev": "sha256:<previous-event-digest>",
    "created": {"physical_ms": 1789243200000, "logical": 0},
    "to": "recipient-participant-id",
    "in_reply_to": "sha256:<event-digest>",
    "body": "short text",
    "artifact": null
  },
  "signature": {
    "algorithm": "ed25519",
    "key_id": "participant-key-id",
    "value": "<unpadded-base64url>"
  }
}
```

Canonicalize the unsigned payload with RFC 8785, sign a domain-separated byte sequence such as `IHR-EVENT-V1\0 || canonical_payload`, and store the signature in an envelope. Hash the canonical signed envelope for the event ID. Validate the complete chain before advancing the contiguous sequence for an author.

Do not require one authoritative total order. Present a stable deterministic view using corrected HLC order and explicit `reply_to` links. A late offline event may appear earlier in the view; expose that fact rather than pretending append order never changes.

### Harness boundary

Start with a tiny versioned strict JSONL interface (`send`, `list`, `wait`, `claim`, `ack`, `history`, `status`). The core remains directly usable through its CLI and imports no harness SDK. Pi extensions, Claude Code hooks, Codex adapters, Grok plugins, MCP wrappers, and other native integrations belong in separate projects; they translate calls but own no replication, persistence, signing, or validation logic.

Run one instance of the same small core sidecar per harness session. Each instance owns one stable `harness_id`, resumable `session_id`, participant identity, queue, cursor, configuration, and lease; store these in its validated nested path and never derive paths from display names. Session sidecars on the same machine share only replicated project history and the content-addressed artifact store, using immutable unique paths and the validated publication operation. This avoids machine-wide registration and multiplexing logic while keeping one executable. Concurrent sessions need distinct participant signing identities so they cannot both advance one author chain.

During per-project setup, require an awareness choice: `addressed` (default, direct and broadcast traffic only) or `all` (the full live conversation). In `all`, label traffic addressed elsewhere as `observed` rather than as a request. Keep full history queryable in both modes and apply mode changes only to future filing, avoiding surprise token-heavy backfills.

Do not make the model poll continuously. The adapter should monitor in the background and offer a blocking `wait`; fallback polling should be rate-limited and return `retry_after_ms`. Batch observed traffic into the next turn rather than waking the agent for every overheard message. Track work with request-linked `accepted`, `progress`, `completed`, and `failed` events, including an optional `next_update_in_ms`. Each session adapter daemon should monitor its harness process through a renewable lease. Exchange signed, TTL-bound `presence` signals as replaceable ephemeral state, never as durable history: liveness says a session lease is renewing, while progress events say whether work is advancing. Keep only two v1 retention classes—durable project events and ephemeral monitoring state.

The current pi-intercom remains useful as UX prior art, not as cross-machine transport. Its own README says it is same-machine by design, uses local sockets/pipes, and keeps only bounded broker-runtime mail for disconnected named sessions. Its extension channel has a 16 KiB cap; that cap does not necessarily apply to every direct-chat path.

Source: <https://github.com/nicobailon/pi-intercom>

## Existing systems: adopt, borrow, or reject

| System | Assessment |
|---|---|
| **Syncthing** | Strong external adapter and conformance oracle for authenticated delta replication. Not required by v1 and never imported or started as a relay child. |
| **Secure Scuttlebutt** | Strongest direct prior art for per-author signed chains, vector-clock replication, and hash-addressed blobs. Borrow the data model; do not adopt its old JavaScript stack without a separate maintenance review. |
| **A2A Protocol 1.0** | Useful future northbound agent/task interoperability: messages, tasks, artifacts, polling, streaming, push, HTTP/REST, JSON-RPC, and gRPC. It does not provide peer replication, offline multi-master history, or no-single-point-of-failure storage. |
| **Matrix federation** | Technically capable of signed federated room history and media, but room state resolution, homeservers, federation authorization, and operational surface are far beyond this small trusted fleet's needs. |
| **NATS/JetStream** | Excellent brokered messaging. Durable replicated JetStream introduces servers, clustering, stream configuration, and quorum concerns; leaf nodes still connect toward hubs. It does not match “every worker writes fully offline” as directly as file replication. |
| **pi-intercom / pi-link / agent-bus family** | Useful local harness UX and routing examples. Current first-party READMEs explicitly describe localhost/same-machine limits and ephemeral or bounded offline behavior. They do not meet this project's durability and cross-machine requirements. |

A2A source: <https://github.com/a2aproject/A2A/blob/main/docs/specification.md>

NATS sources: <https://docs.nats.io/running-a-nats-service/configuration/leafnodes> and <https://docs.nats.io/nats-concepts/jetstream>

Matrix source: <https://spec.matrix.org/latest/server-server-api/>

pi-link source: <https://github.com/alvivar/pi-link>

## Corrections applied to the decision log

1. The label “official Pi extension” for `pi-intercom` is not supported by the evidence reviewed. The project is under a personal GitHub account, describes itself as an extension for Pi, and is not mentioned in the installed official Pi documentation. Call it a community Pi extension unless an official endorsement is cited.
2. The 16 KiB statement needs narrowing. The current README applies 16 KiB to `channel.publish()` for extension-channel payloads; it describes a separate frame-size cap for broker messages but does not state here that all direct messages are capped at 16 KiB.
3. Popularity counts in `DECISIONS.md` are snapshots and are already stale. On 2026-09-12 the GitHub API reported `nicobailon/pi-intercom` at 509 stars and 87 forks, and `MustaphaSteph/agent-bus` at 17 stars. Either date every count or omit counts and evaluate capabilities/maintenance evidence instead.
4. “No entry is ever lost as long as at least one live copy exists somewhere” requires eventual connectivity, retention, correct anti-entropy, and a holder that remains available long enough to replicate. State those assumptions.
5. The history is a grow-only event set with a derived order, not a single append-only ordered log in the Kafka sense. Kafka's comparison is useful for pull consumption and claim-check payloads, but it should not imply Kafka-like partition ordering.

GitHub API source for pi-intercom: <https://api.github.com/repos/nicobailon/pi-intercom>

## Decisions resolved by the 2026-09-14 alignment

1. The no-loss goal includes eagerly copied whole artifacts.
2. Delivery provides at-least-once presentation with event-ID deduplication and
   visible recovery state.
3. V1 has one project administrator authorized to change membership, replace
   keys, close, and reopen. Purge is a confirmed local operation.
4. Old-epoch events remain stale, non-actionable history and must be republished
   deliberately in the current epoch.
5. V1 uses Go TLS 1.3 and per-author reconciliation. Syncthing is optional and
   independently operated, not a required dependency.

## Go Dependency Evaluation (2026-09-14)

The dependency gate recommends **one required runtime library**, **one likely concurrency library**, and no framework:

1. **Adopt `github.com/gowebpki/jcs` for RFC 8785 canonicalization.** Its `Transform([]byte)` interface canonicalizes the exact received JSON bytes and rejects duplicate keys before signing or verification. Version `v1.0.1` is Apache-2.0, has no runtime dependency, and derives from the RFC author's reference implementation and test vectors. Retain local RFC-vector tests at the wrapper seam. This is security/interoperability code that the project should not recreate. Sources: <https://github.com/gowebpki/jcs>, <https://pkg.go.dev/github.com/gowebpki/jcs>, and <https://www.rfc-editor.org/rfc/rfc8785>.
2. **Use `golang.org/x/sync/errgroup` if the approved sidecar lifecycle has multiple error-returning goroutines.** It provides cancellation on the first error, error propagation, and bounded concurrency through `SetLimit`, avoiding a custom supervisor. It is maintained by the Go project under BSD-3-Clause. Select a release compatible with the project's eventual minimum Go version; for example, `x/sync` v0.22.0 requires Go 1.25 while v0.23.0 requires Go 1.26. Source: <https://pkg.go.dev/golang.org/x/sync/errgroup>.

**Defer `github.com/fsnotify/fsnotify`.** It supports Linux, macOS, and Windows, but it is not recursive, does not work on NFS/SMB, and documents platform-specific missed-event/resource limitations. Correctness still requires reconciliation scans, so adding notifications initially creates two paths without eliminating the scan. Reconsider only if measured scanning cost or delivery latency requires it. Source: <https://github.com/fsnotify/fsnotify>.

Use the standard library instead of third-party packages for CLI parsing (`flag`), configuration (`encoding/json`), logging (`log/slog`), TLS/Ed25519/SHA-256, IDs, backoff, rate limiting, filesystem publication, HTTP, and tests. Do not initially add Cobra/Viper, Zap/Zerolog, Testify, UUID/ULID, database, CRDT, libp2p, OpenTelemetry, Prometheus, file-lock, retry, dependency-injection, or plugin libraries.

Use standard `testing/synctest` when the minimum Go version supports it; Go 1.25 added its isolated concurrent-test bubble and virtual clock. Use the official `govulncheck` command in verification rather than importing it into the product. Sources: <https://pkg.go.dev/testing/synctest> and <https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck>.

This environment did not have the `go` command installed on 2026-09-14, so API and metadata findings were verified from upstream source, pkg.go.dev, the Go module proxy, and official documentation but were not executed locally.

## Local research material

The component-by-component follow-up is in [`PRIOR_ART.md`](PRIOR_ART.md). It records the bounded Ideate→Refute query process, project reviews, harness-specific findings for Claude Code, Codex, Grok, and Pi, distilled invariants, and confirmed gotchas.

Raw search responses, official documentation pages, RFC text, repository README/API metadata, and extracted text remain in a local research archive. The archive is excluded from the public repository because it contains third-party snapshots and internal model logs. These materials are research evidence, not normative vendored dependencies.

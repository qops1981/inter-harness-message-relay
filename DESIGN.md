# Cross-Machine Agent Messaging — Design Document

## 1. Background & Motivation

This system grew out of a concrete, narrow problem: an orchestrator agent and
several worker agents, each running under potentially different harnesses
(initially Claude Code, Codex, Grok, and Pi) on **separate physical machines**,
needed to pass messages to one another — including large artifacts like code
reviews — with no single point of failure, and with every participant able to
see the shared history even when a given message wasn't addressed to them.

The design below is the result of working backward from that requirement
through several rejected approaches, each of which taught us something about
what the problem actually was (and wasn't). See `DECISIONS.md` for the full
reasoning trail. This document describes the resulting architecture.

## 2. Goals

- Any-to-any messaging between one orchestrator and N worker agents (N may
  grow over time).
- Works across distinct physical machines, not just processes on one host.
- **No single point of failure** — every participant holds a full, working
  copy of shared state, and can operate while disconnected.
- **Configurable shared awareness** — every participant can query the full
  project history, while each agent chooses during setup whether its live
  inbox monitors only addressed traffic or the full conversation.
- Support for large message bodies (e.g., code reviews, diffs) without
  bloating the replicated history.
- **Harness-agnostic** — must not assume Pi, Claude Code, or any other
  specific agent runtime; the core exposes one stable local interface and never
  imports a harness SDK. Harness-specific bridges are separate projects.
- **Network-topology-agnostic** — works over a LAN or the public Internet;
  a VPN or private mesh may add defense in depth but is never required.
- **Portable core** — the daemon is implemented in Go and distributed as a
  native binary for Linux, macOS, and Windows. Optional operator scripts use
  portable POSIX `sh` on Linux/macOS; core behavior never depends on a shell,
  Ruby runtime, or platform-specific command.
- Storage scoped per-project, so a project's entire footprint (messages,
  queue state, artifacts) lives in one place and can be archived or purged
  as a unit.
- **Storage over processing** — accept duplicate local and cross-machine data
  when it avoids coordination, reconstruction, network round trips, or agent
  work. Content addressing provides identity and integrity; eliminating every
  duplicate is not a goal.
- Simple enough to actually build and reason about — deliberately avoiding
  machinery designed for problems this system doesn't have (see §4).
- A versioned developer guide is a release deliverable. It explains how a
  harness bridge starts a session sidecar, exchanges messages, handles
  delivery and liveness, and remains compatible with the core. Idiomatic Go
  doc comments and runnable `Example` tests keep the public contract beside the
  code and verify its primary flows.
- The project dogfoods the relay as soon as the minimal direct-message path
  works. Early use runs beside an independent fallback channel until recovery
  and cross-machine behavior are proven.
- Installation supports two small paths: a standalone core-first guide and an
  integration-led guide that lets the harness agent perform mechanical setup
  after no more than five explicit operator approvals. Existing compatible core
  installations are reused.

## 3. Non-Goals

- **Byzantine fault tolerance / adversarial trust.** All participants
  (orchestrator + workers) are assumed to be mutually trusted. This is not a
  system for parties who need to agree on truth *despite* not trusting each
  other.
- **Global scale.** This is designed for a handful of machines (one
  orchestrator, a few to a few dozen workers), not thousands of consumers.
- **Guaranteed real-time delivery.** The system is pull-based and
  eventually-consistent by design; it tolerates offline participants rather
  than guaranteeing sub-second delivery.
- **General-purpose agent platform.** This tool relays authenticated,
  project-scoped messages and artifacts and reports bounded presence. Harnesses
  own agent execution and process lifecycle. Scheduling, orchestration, service
  management, workflow execution, dashboards, plugin hosting, and general
  database/message-bus features stay outside the core.

## 4. Architecture Overview

The system has three logical layers, all scoped **per project**.

```
projects/<project_id>/
  events/<participant_id>/     # immutable signed author chains
  artifacts/sha256/            # verified whole-artifact copies
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
  archived                     # derived marker for a closed epoch
```

### Layer 0 — Replicated Project History

- Project history is a set of immutable signed **per-author event chains**, not
  one physically ordered multi-writer log.
- Each event has an author sequence and previous-event hash. Its ID is the
  SHA-256 digest of its RFC 8785-canonical signed envelope.
- Every participant with an interest in an active or retained project keeps a
  full local copy. There is no leader or central broker.
- Peers exchange the highest contiguous sequence accepted for each author and
  request missing ranges. One global ID, timestamp, or display position is
  never a replication cursor.
- A corrected HLC tuple `(physical, logical, author, author sequence, event ID)`
  gives replicas a deterministic display view. Explicit reply/request links
  carry causality, and late offline events can appear earlier in the view.
- Gaps wait for their preceding events. A conflicting event at an existing
  author sequence is a detectable fork and is quarantined and reported.
- A disconnected participant can append to its own chain and reconcile on
  reconnect, provided its project epoch and signing key remain current.

This is a **crash-fault-tolerant replicated history**, not a consensus system in
the Raft/Paxos sense (no quorum writes, no leader) and not a blockchain (no
proof-of-work/stake, no adversarial trust model). See `DECISIONS.md` §2–3 for
why those heavier models were considered and rejected.

### Layer 1 — Session Delivery

- Each sidecar stages a complete delivery under `tmp` before publishing it to
  that session's `new` queue.
- Delivery moves through `new`, `processing`, and `done` or `failed`. A bounded
  claim lease returns abandoned `processing` work to `new`, so delivery attempts
  are at least once and can repeat.
- Acknowledgment means the harness accepted the delivery. Work completion is a
  separate durable event.
- Queue, cursor, claim, and awareness state live under validated
  `harness_id/session_id` path segments, so sessions never share delivery state
  accidentally.
- Project history remains authoritative. If local delivery state is lost, the
  queue can be rebuilt with safe redelivery; it cannot remove durable history.
- Staging and publication remain on one filesystem. The platform contract must
  not claim stronger atomicity than Go and the target filesystem provide.

### Artifact Store

- Large content (a code review, a long diff) is **never placed in an event
  body**. The event carries a descriptor with SHA-256 digest, byte size, and
  media type.
- The digest covers raw bytes without timestamps, permissions, filenames, or
  stored compression. Receivers check the size bound before hashing.
- The artifact is named by its hash and replicated with the project so every
  participating machine can retain a complete usable copy without depending on
  the origin remaining online.
- Each interested session may materialize its own artifact copy inside
  `queue/.../<id>/artifact`. This deliberate duplication avoids shared-read
  coordination and makes each session directory self-contained.
- Event history contains only the artifact descriptor, keeping history cheap to
  scan even though artifact bytes are stored redundantly.

### Local Adapter Process (One Per Harness Session)

Each harness session runs the same small adapter daemon as a sidecar. An
instance owns exactly one `harness_id`, resumable `session_id`, queue, cursor,
configuration, lease, and participant signing identity. It does not maintain a
registry or multiplex unrelated sessions. Raw display names are never used in
paths.

All session adapters on a machine share replicated project history and the local
content-addressed artifact store. Immutable unique paths and one validated
publication operation prevent conflicts between those instances. A machine-level aggregator
is deferred unless measured process or connection overhead justifies it.

The core sidecar exposes a harness-neutral strict JSONL interface. A separate
harness bridge may translate native Pi, Claude, or other tool calls into that
interface, but it owns no replication, storage, cryptography, or protocol
logic. The core also remains usable directly from its CLI without a bridge.

Each sidecar connects its harness session to the shared system:

- **Write path:** the agent hands off an outgoing message → the sidecar
  validates and publishes any artifact → allocates the next author sequence →
  signs and publishes the current-epoch event.
- **Read path:** the sidecar tracks a local filing cursor and files events according
  to the agent's project-local awareness mode:
  - `addressed` (default): direct messages and broadcasts only;
  - `all`: every message, with non-addressed traffic clearly labeled
    `observed` rather than presented as a request to answer.
  The agent can query full history in either mode. Changing mode affects future
  filing; old traffic remains available through history without causing an
  automatic token-heavy backfill. A bounded wait claims a delivery, and an
  explicit acknowledgment completes it. An expired claim can redeliver.

### Notifications, Progress, and Liveness

The adapter—not the model—monitors replicated state continuously. A harness
should wait for an adapter notification instead of spending turns polling. If a
harness only supports pull, the adapter exposes a blocking wait; a non-blocking
poll response includes `retry_after_ms` and enforces a configurable minimum
interval.

An observed message in `all` awareness mode does not wake the agent by default.
It is batched into the next agent turn. Direct messages and broadcasts may wake
the agent according to local harness policy.

Work requests use small durable status events linked to the request:
`accepted`, `progress`, `completed`, or `failed`. `accepted` and `progress` may
include `next_update_in_ms`, which tells collaborators how long to wait before
checking again without depending on synchronized wall clocks. Conversation
history is reconstructed through event references; it is not copied into every
message.

Liveness is ephemeral adapter state, not durable conversation history. Each
session adapter daemon monitors its harness process through a renewable lease,
without invoking the model. Adapters exchange signed,
replaceable `presence` signals with a bounded `lease_ms`; receivers expire them
using elapsed time since receipt. A heartbeat proves only that the
adapter/session lease is alive; task progress is proved by durable status
events. Peers report `online`, `offline`, or `unknown` rather than treating a
missed heartbeat as proof of failure.

The protocol has two retention classes: durable project events remain until
explicit purge, while ephemeral monitoring signals expire and never enter the
project history or agent queue. Correctness and work results never depend only on
ephemeral state.

### Runtime Observability

Each sidecar writes structured JSON Lines logs to `stderr`. Lifecycle,
authentication, validation, delivery, reconciliation, recovery, and failure
transitions carry existing correlation identifiers so an operator can follow an
operation across machines. Logs never contain message bodies, artifact content,
private keys, credentials, or authentication material. The caller owns capture,
rotation, and retention.

The existing `status` CLI/stdio operation returns a bounded metrics snapshot:
uptime, queue depth, message outcome counters, peer and presence state,
reconciliation outcomes, pending event/artifact counts, transferred bytes, lease
freshness, and the last successful reconciliation and sanitized operational
error. The sidecar does not expose a metrics listener or monitoring server.

Dedicated distributed tracing is deferred. Request, event, reconciliation, and
delivery IDs provide lightweight correlation in logs. Add a tracing framework
only when recorded diagnostic evidence shows that correlation cannot explain a
real cross-machine failure.

## 5. Project Control and Lifecycle

- A project is a self-contained directory and has one project administrator in
  v1, which avoids conflicting offline control histories. Initial administrator,
  membership, epoch, and transport trust are installed out of band. The operator
  keeps an offline recovery copy of the administrator key; losing it blocks
  control changes but not current-epoch messaging or history reads.
- Administrator-signed control events add or revoke participants, replace lost
  keys, rotate the administrator, close the project, or reopen it. Each accepted
  control event advances the project epoch exactly once.
- Ordinary events name their project epoch. After a replica learns a newer
  epoch, an old-epoch event remains visible as stale history but is not
  delivered or executed; its author must republish current intent. A partitioned
  replica can act before learning a close or revocation because this design has
  no global consensus barrier.
- Closing archives the project and stops ordinary writes and filing. It does not
  delete bytes or stop bounded reconciliation, because replicas must still learn
  an authorized reopen.
- Reopen is an administrator control event, not a local marker edit.
- Purge is a separate, confirmed local operation. V1 has no automatic age purge
  or artifact garbage collection.

## 6. Trust & Security Model

- All participants are mutually trusted (owned/operated by the same party);
  this is a crash-fault-tolerant design, not a Byzantine-fault-tolerant one.
- Every event is signed (Ed25519) by its author, so project history can
  verify authenticity/integrity of who wrote what — important once the
  orchestrator (or any node) is relaying messages on behalf of others, not
  just receiving for itself.
- V1 uses mutually authenticated TLS 1.3 with out-of-band configured trust.
  Machine transport identity remains separate from participant event identity.
  The service is safe without a VPN; WireGuard, Tailscale, or a trusted LAN are
  optional defense-in-depth and routing choices.
- TLS authenticates a connection, not project authority. The application still
  checks membership, project epoch, replay/duplicate state, signatures, chains,
  and artifact hashes. Peer addresses are configured; discovery and ICE remain
  outside the core.
- Event signatures preserve author authenticity and integrity after relay or
  storage. They do **not** provide confidentiality; confidentiality comes from
  the required encrypted transport. End-to-end payload encryption independent
  of the transport remains a possible later feature.

## 7. Open Questions

- Literal v1 bounds for stdio records, inline bodies, artifacts, batches,
  pending claims, and waits must be selected in the interface PPP.
- The implementation PPP must state and test the filesystem durability contract
  for Linux, macOS, Windows, and any supported network filesystem.

## 8. Prior Art & Inspiration

See `DECISIONS.md` for the full comparative analysis. In summary, this
design deliberately borrows the *shape* of several existing systems while
avoiding machinery those systems need for problems we don't have:

- **Apache Kafka** — log + claim-check pattern (small message in the log,
  large payload referenced by pointer and fetched separately).
- **Maildir, Postfix, Beanstalkd, and NATS** — hidden staging, bounded active
  work, explicit claim/ack state, claim expiry, and safe redelivery without an
  exactly-once promise.
- **Git** — a project as a fully self-contained directory; deleting the
  directory deletes the whole history; distributed copies as a natural
  backup property.
- **Secure Scuttlebutt and Syncthing BEP** — signed per-author chains,
  per-author reconciliation cursors, explicit gaps, and reset identities
  without a leader or voting round.
- **OpenViking** (openviking.ai) — "everything is addressable through a
  filesystem-like paradigm" as a design philosophy for agent context/memory,
  which validated treating messages and artifacts as directories/files
  rather than opaque database rows.
- **pi-intercom, inter-agent-pi, Agent Comms, agent-bus, agent-bus-team** —
  existing small/solo-maintained agent-messaging tools that were evaluated
  directly as candidates before this design was undertaken; each was
  found to be either single-machine-only or too immature/unsupported to
  depend on, which is what motivated designing this system rather than
  adopting one outright.

## 9. Development Reference

The relay is one bounded context with four cohesive domain areas. These areas
are conceptual responsibilities, not separate processes or mandatory Go
packages. Canonical terms are defined in `CONTEXT.md`.

### External Shape

```text
harness-specific bridge (separate project)
                |
        versioned stdio/JSONL
                |
      per-session relay sidecar
                |
    +-----------+-----------+
    |           |           |
 project     session     presence
 history     delivery    and leases
    |           |           |
    +------ reconciliation -+
                |
       filesystem + peer transport
```

The direct CLI uses the same core operations as a bridge. It does not create a
second implementation path.

### Domain Areas

**Project history** owns projects, participants, events, messages, work status,
artifacts, project epochs, canonicalization, signing, validation, and durable
history invariants.

**Reconciliation** owns replicas, peers, author sequences, reconciliation
cursors, authenticated exchange, reconnect behavior, missing-event transfer,
and artifact replication. It has no harness-specific behavior.

**Session delivery** owns harness/session identity, the per-session sidecar,
delivery relations, awareness mode, queue state, session cursors, waiting,
claiming, and acknowledgment.

**Presence** owns session leases and ephemeral presence signals. It remains
separate from project history because its retention and correctness rules are
different.

The identity model is deliberately explicit:

```text
Machine != Harness != Session != Participant
```

A machine can run multiple harnesses; a harness can own multiple sessions; each
concurrent session has a distinct participant identity. A participant can
resume or move, but only one session can write its author sequence at a time.

### Initial Go Layout

Start flat and deepen only when a second real implementation creates a seam:

```text
cmd/relay/main.go
internal/relay/
  event.go
  project.go
  identity.go
  store.go
  delivery.go
  presence.go
  reconcile.go
  stdio.go
```

Tests live beside the code they exercise. Do not start with `pkg/`, service and
repository layers, factories, or an interface for every file. Files can become
packages later when doing so removes demonstrated caller complexity.

### Go Implementation Posture

Use one Go process with a bounded set of long-lived goroutines. `context.Context`
coordinates cancellation and deadlines; channels carry small internal handoffs,
not a second message bus. Prefer single-goroutine state ownership, then
`sync.WaitGroup`, mutexes, or atomics only where ownership cannot stay local.

Use Go's standard cryptography, TLS, JSON, filesystem, time, structured logging,
and testing packages. Publish immutable files through exclusive creation or a
synced same-filesystem temporary file plus a platform-verified publication
operation. Use typed identifiers, bounded
input readers, explicit protocol versions, and wrapped/classifiable errors at
trust boundaries. A focused RFC 8785 library is the expected exception to the
standard-library rule.

Built-in tests use temporary directories, in-memory connections, HTTP test
servers where applicable, examples, fuzz targets, and the race detector. Use
`testing/synctest` for virtual-time concurrency tests when supported by the
minimum Go version.

The one approved security dependency is `github.com/gowebpki/jcs` for RFC 8785
canonicalization, wrapped behind a small internal function and checked against
RFC vectors. Use `golang.org/x/sync/errgroup` only when multiple error-returning
goroutines make custom cancellation/error propagation necessary. Defer
`fsnotify`: notifications cannot replace correctness scans and initially create
a second path. Syncthing, if selected, remains an external executable rather
than an imported implementation dependency.

Avoid CGO, `unsafe`, plugins, reflection-heavy frameworks, speculative generics,
unbounded goroutines, and build tags that do not isolate an unavoidable
platform difference.

### Outside-In Delivery Order

Specify and accept one public CLI/stdio behavior at a time, then let Forge build
its internals inside-out:

1. Two local sessions send, receive, claim, and acknowledge one signed message.
2. An offline session receives that message after restart or reconnect.
3. Two machines reconcile the same project history.
4. An artifact replicates and verifies.
5. Awareness modes and observed-message batching work.
6. Presence and work-progress signals work.
7. Project close and reopen work.

Each slice must be usable through the public seam before the next slice expands
the implementation. This order supplies the dogfood activation path without
building speculative infrastructure first.

# Cross-Machine Agent Messaging — Decision Log

Purpose of this document: capture *why* the design in `DESIGN.md` /
`SPEC.md` looks the way it does — including the alternatives that were
considered and rejected along the way. An agent picking this project up
cold should be able to read this and understand not just the shape of the
system, but the reasoning that ruled out simpler and more complex
alternatives, so as not to relitigate settled ground unnecessarily.

---

## D1: Existing agent-messaging tools were evaluated first and found
insufficient

Before designing anything new, the following existing projects were
researched as candidates:

- **pi-intercom** — community Pi extension. Each session connects to a local
  broker over local IPC. **Limitation: local-machine only.** Its 16 KiB limit
  applies to the documented extension-channel operation, not necessarily every
  direct-message path.
- **inter-agent-pi / inter-agent-core** — a networked alternative
  (host/port override, shared-secret auth, TLS support), but maintained by
  a single developer, recently split out of a private monorepo with no
  history of independent adoption. No evidence of community traction.
- **Agent Comms** (ExaDev) — explicitly cross-harness (Claude Code, Pi,
  etc.), TCP-based. 19 stars, 6 forks, 0 watchers at time of research. Had
  already undergone one breaking protocol change (v1→v2) with no migration
  path. Local-machine (localhost) scope only.
- **agent-bus** (MustaphaSteph) — 13 stars, 0 forks, MIT licensed,
  reasonable docs, but small/new, local-machine scope.
- **agent-bus-team**, **dataforxyz/agent-intercom-*** family — similarly
  small, fast-iterating, pre-1.0 solo projects, local-machine scope.

**Conclusion:** the cross-harness *adapter* problem (getting Claude Code, Pi,
Codex, etc. to speak a common protocol) is already solved multiple times
over in the small-tool ecosystem. What's consistently missing across every
option is the **cross-machine transport** — every one of these tools
assumes localhost. None of the candidates were both harness-agnostic *and*
cross-machine, and none had the maturity/support to justify depending on
for a multi-machine setup. This is what justified designing a system rather
than adopting one.

---

## D2: Distributed ledger — considered and rejected

**Status:** the rejection of consensus remains current. D26 supersedes the
statement below that hash chains were only optional; research established
per-author hash chains as the simplest valid replication unit.

**Proposal considered:** use a distributed ledger / blockchain-style
structure for the shared message history.

**Why rejected:** a distributed ledger's consensus machinery (proof-of-work,
proof-of-stake, Byzantine fault tolerance, multi-round voting) exists to
solve one specific problem: **multiple parties who do not trust each other
need to agree on a single history without a central authority.** This
system's actual trust model is the opposite — one owner, one orchestrator,
N workers, all mutually trusted. There is no adversarial party disputing
message order, and no need for economic or cryptographic consensus
mechanisms to prevent double-spend-style disputes. Adopting ledger machinery
would import significant complexity to solve a trust problem that doesn't
exist here, while leaving the actual problem (transporting messages between
machines) completely unsolved — a ledger does not, by itself, get a message
from machine A to machine B.

**What was kept from the idea:** the notion of a **hash-chained per-author
feed**. D26 adopts this in v1 because the previous hash and author sequence
provide the gap and fork checks needed for correct offline reconciliation.
Consensus, mining, voting, and a global chain remain rejected.

---

## D3: Single shared database — considered and rejected

**Proposal considered:** once "everyone writes to the same history" was
agreed, a natural next step was a single shared database (e.g., Postgres or
a shared SQLite instance) that all participants read/write to directly.
Standard multi-writer database transaction handling already guarantees a
single consistent, ordered history for concurrent writers — this is a much
older and simpler problem than either ledger consensus or leaderless
replication.

**Why rejected:** an outage of that single service is an outage for every
participant — it reintroduces exactly the single point of failure the
overall design was meant to avoid, and it doesn't provide the "multiple
identical copies" property that was an explicit requirement (each
participant should be able to keep functioning fully, including offline,
on a flaky or partitioned link).

**What was kept:** the recognition that this system does not need
Byzantine-tolerant consensus — only **crash fault tolerance** among
mutually trusted nodes. That distinction (crash-tolerant vs.
Byzantine-tolerant) directly informed the choice in D4.

---

## D4: Leader-election replication (Raft/Paxos-style) vs. per-author
anti-entropy — leaderless reconciliation chosen

Two crash-fault-tolerant replication strategies were compared:

1. **Leader-election replication** (e.g., via etcd, or NATS JetStream in
   clustered mode) — strong consistency, automatic failover via leader
   election, but requires 3+ nodes for meaningful quorum, and is briefly
   unavailable during leader election.
2. **Leaderless per-author reconciliation** — every node keeps its own full
   copy, writes to its participant's signed author chain while offline, and
   exchanges the highest contiguous sequence per author with reachable peers.
   HLC produces only a deterministic display view; it is not a merge cursor.

**Decision: leaderless per-author reconciliation chosen**, because it more
directly matches the stated requirement ("multiple identical copies," works
with as few as two machines, no quorum requirement, no leader-election
unavailability window) and because a handful of trusted machines does not need
the stronger consistency guarantees Raft-style systems are built for. D26
defines gap, duplicate, and fork rules.

---

## D5: Kafka comparison — pattern adopted, machinery scaled down

Midway through the design, the resemblance to **Apache Kafka** was noted
explicitly and used as a sanity check:

- Kafka's immutable records inform Layer 0, but the relay uses one ordered
  chain per author rather than one broker partition order.
- Kafka's consumer offsets inform local delivery state, but cannot serve as
  the relay's multi-author reconciliation cursor.
- Kafka's pull-based consumption model maps directly to "the agent asks for
  new messages; nothing is pushed uninvited."
- Kafka's standard **large-payload pattern is the same "claim check"
  idea** used here: don't put the large object in the log; put a reference
  to it (typically in object storage) in the log message instead. This
  system arrived at that pattern independently before the Kafka comparison
  was made, which was treated as validation that the pattern is
  well-established rather than idiosyncratic.

**Where this system deliberately diverges from Kafka:** Kafka assumes a
reliably-available broker cluster and clients are just readers/writers over
the network. This system instead requires every node to hold a **full local
replica** that keeps functioning fully offline — a stronger property than
Kafka's broker-durability model provides by default. Kafka's
partition/controller machinery (built for scaling to many brokers and many
consumers) was judged unnecessary at this system's scale (a handful of
machines) and was replaced with the per-author anti-entropy from D4 and D26.

---

## D6: Large content — kept out of event bodies, "claim check" pattern

**Research finding:** existing comparable tools cap message size
deliberately small — pi-intercom's extension API caps payloads at 16 KiB;
independent guidance on building Pi-to-Pi buses recommends capping at 2 KB
specifically to avoid runaway token/loop costs. None of the evaluated
cross-harness tools (Agent Comms, agent-bus, agent-bus-team) advertise
large-payload or streaming support.

**Decision:** rather than raising the inline event-body limit, large content
(e.g., a code review) is stored as artifact bytes outside project history, while
an event carries its small descriptor. This mirrors
accepted practice for agent-to-agent protocol design generally: pass
references to data stored elsewhere rather than embedding large artifacts
directly in messages, and support chunking/streaming only if and when
payload sizes actually demand it (not needed at the sizes anticipated here).

---

## D7: Content-addressed artifact store, replicated eagerly

**Decision:** artifact descriptors contain a SHA-256 digest, byte size, and
media type. The digest covers raw bytes and excludes filenames, timestamps,
permissions, and stored compression. Receivers check size bounds before hashing
and compare the verified digest with the event descriptor. Artifacts are eagerly
replicated with the project so every participating machine can retain a complete
usable copy without depending on the origin remaining online. A missing
artifact may be obtained from any holder.

This spends storage and transfer bandwidth to remove availability coordination
and later network fetches. Content addressing remains valuable for identity and
verification; implementations are not required to eliminate every duplicate.

---

## D8: Directory-per-message layout, inspired by Maildir and OpenViking

**Proposal:** rather than storing messages as opaque rows in a database,
represent each delivery as a directory (`meta.json` + optional `artifact`
file), and represent local delivery with `tmp`, `new`, `processing`, `done`,
and `failed` directories.

**Inspirations, explicitly:**
- **Maildir** (the email storage format) — hidden staging followed by
  publication of one complete immutable item. Maildir does not provide
  exactly-once work execution or identical flush guarantees on every platform.
- **Beanstalkd, NATS, Postfix, and AWS idempotency practice** — bounded active
  work, claim expiry, explicit acknowledgment, stable request IDs, and safe
  redelivery.
- **OpenViking** (openviking.ai) — an open-source context database for AI
  agents that stores memories, resources, and skills as one virtual
  filesystem under a `viking://` URI scheme, letting an agent browse its
  own context with `ls`/`tree`/`find` rather than querying an opaque
  store. This validated the broader idea of making agent-facing state
  filesystem-native and directly inspectable, which is why the same
  philosophy was applied to messages/artifacts here.
- General Unix philosophy ("everything is a file") as the underlying
  cultural precedent for both of the above.

**Delivery contract:** v1 provides at-least-once presentation. A claim moves a
ready delivery to `processing` with a bounded lease; restart or expiry returns
it to `new`. Acknowledgment moves it to `done`, while explicit failure moves it
to `failed`. Queue loss can cause safe redelivery. Staging and publication stay
on one filesystem, and platform tests define the actual durability guarantee.

**Tradeoff acknowledged:** storing the artifact directly inside each
session's delivery directory as well as the machine's content-addressed artifact
store sacrifices some deduplication. This is deliberate: self-contained session
directories and cheap local reads are worth more than disk savings at the
anticipated scale. Replicated project history contains only the descriptor, so
duplicate artifact bytes do not make history expensive to scan.

---

## D9: Project-scoped storage, inspired by Git's self-contained-repo model

**Decision:** each project gets its own replicated event history and directory
tree (`projects/<project_id>/...`), rather than one global log for all
projects. This was chosen so that:
- Machines only sync/store history for projects they actually participate
  in (bounded storage/sync cost per machine).
- A project's entire footprint — messages, queue state, artifacts — is
  self-contained in one directory, echoing how a Git repository is a
  self-contained unit whose deletion removes its whole history.

---

## D10: Close vs. delete — replication's "flaw" reframed as a safety
feature

**Initial concern raised:** if "closing" a project causes every machine to
delete its local copy, deletion is not atomic across machines — a machine
that hasn't yet processed the close/delete broadcast still has a copy.

**Reframing (accepted):** rather than treating this as a bug to close, it
was recognized as a naturally-occurring backup property of the replication
model already in place — exactly the "multiple identical copies" resilience
this system was designed for in the first place, just manifesting as
protection against accidental/premature deletion rather than only against
machine failure.

**Decision:** **closing ≠ deleting.** An administrator-signed close control
event advances the project epoch and causes every participant to *archive* its
local copy and stop ordinary writes and
session filing rather than purge it immediately. Bounded reconciliation
continues so replicas can learn an administrator-authorized reopen. Actual
deletion is a separate, deliberate, rarer operation (e.g., a periodic purge
of archived projects past some age), decoupled from the close event. This
gives an accidental or premature close a recovery path via any surviving
archived copy, and treats redundancy as a deliberate safety property rather
than an incidental side effect to eliminate.

**Purge policy:** v1 has only explicit, confirmed local project purge. It has
no age-based automatic purge or unreferenced-artifact garbage collection.

---

## D11: VPN/private mesh — optional, not required

**Decision:** the service must work over either a trusted LAN or an untrusted
network without requiring WireGuard, Tailscale, or another VPN. The selected
machine-to-machine transport must provide authenticated encryption itself (for
example, TLS 1.3 with peer authentication).

A VPN or private mesh remains a valid operator choice for routing, exposure
reduction, and defense in depth, but it is not part of the protocol and must not
be a prerequisite. Event signatures and artifact hashes are still verified above
the transport because data can be relayed or stored after its original
connection ends.

V1 selects mutually authenticated TLS 1.3 from Go's standard library. Trust
anchors or exact peer certificate/public-key identities are configured out of
band; a public CA is not required. Transport identity is separate from event
author identity. TLS does not replace project authorization, epoch, replay,
chain, signature, or artifact validation. Configured peer addresses replace
ICE, global discovery, and mesh routing.

Application-level end-to-end payload encryption, independent of the transport,
is deferred for v1.

---

## D12: Per-agent awareness mode

**Decision:** each agent chooses a project-local awareness mode during setup:

- `addressed` (default) monitors direct messages and broadcasts only. This
  minimizes token use and chatter.
- `all` monitors the full live conversation. Messages addressed elsewhere are
  labeled `observed` so awareness is not mistaken for a request to answer.

The complete project history is replicated and queryable in either mode; this
setting changes local filing and context consumption, not durable data. A mode
change applies to future events. Earlier events remain available through an
explicit history query rather than being automatically injected as a large
backfill.

---

## D13: Adapter-driven notification, explicit progress, and ephemeral presence

**Decision:** agents do not spend model turns continuously polling. The local
adapter monitors state and notifies or wakes the harness only when local policy
allows it. Pull-only harnesses use a blocking wait; ordinary polls receive and
must honor a rate-limited `retry_after_ms`.

Traffic observed through `all` awareness mode is batched into the next turn and
does not wake the agent by default. This preserves full-conversation awareness
without making every side conversation consume a new turn.

Work activity is communicated through durable, request-linked `accepted`,
`progress`, `completed`, and `failed` events. A worker can publish
`next_update_in_ms` so collaborators know how long to wait without depending on
synchronized wall clocks. Response-time history is derived from project events instead
of repeated in every message.

Each session adapter daemon monitors its harness process through a renewable
lease and exchanges signed, TTL-bound `presence` signals
without invoking a model. Presence is replaceable ephemeral state and is
excluded from the append-only project history to avoid permanent noise. The
protocol deliberately has only two v1 retention classes: durable project events
and ephemeral monitoring state. Presence reports `offline` after explicit disconnect and uses one bounded grace
interval before `unknown` after missed renewal or transport loss. Liveness is
not treated as proof of work progress. A dropped
worker is reported but its work is not automatically reassigned, because the
original attempt may have produced non-idempotent effects.

---

## D14: One adapter daemon per harness session

**Decision:** every harness session runs its own instance of the same small
adapter daemon. Local delivery state is nested under
`harnesses/<harness_id>/sessions/<session_id>/` inside each project. Queue,
cursor, awareness, wake configuration, lease, and failures are therefore
isolated when many Pi, Claude, or other harness processes share a machine.

Multiple runtime processes do not duplicate code: they reuse one executable.
This is simpler than a machine-wide daemon that must register, multiplex,
authorize, recover, and route among unrelated sessions. An instance uses stdio
or a session-specific socket rather than competing for a fixed machine port.

`machine_id` identifies the host, `harness_id` identifies the local harness type
or installation, and resumable `session_id` identifies its execution context.
These are adapter-issued or strictly validated opaque path segments; human
display names are metadata, never directory names.

Each concurrent session uses a distinct participant identity/signing key. All
session adapters share the machine's replicated project history and
content-addressed artifact store, using immutable unique event paths and the
validated publication operation. A participant can move between machines or resumed sessions only
through explicit identity transfer after its old lease ends. This avoids
simultaneous writers corrupting a participant's per-author event sequence.

A machine-wide aggregator is deferred until measured process or connection
cost demonstrates a need. The nested directory hierarchy is retained because it
is easier to validate, inspect, archive, and remove without collision.

---

## D15: Storage is cheaper than processing and coordination

**Decision:** when storage and processing trade off, prefer complete copies,
simple sequential reads, and rebuildable derived state. Duplicate events,
artifacts, session queues, and cross-machine replicas are acceptable when they
avoid coordination, reconstruction, network round trips, or model work.

Content hashes are retained for identity and integrity, not as a requirement to
implement global deduplication. Compression, garbage collection, tiered
storage, and sophisticated deduplication are deferred until measured storage
cost justifies their processing and operational complexity. Explicit project
purge remains the simple storage bound for v1.

---

## D16: Go core, optional POSIX-shell helpers

**Decision:** implement the core adapter daemon in Go and release native
binaries for Linux, macOS, and Windows. The daemon's networking, TLS,
cryptography, concurrency, timers, process supervision, and crash-safe
filesystem behavior are beyond the point where Bash remains the simpler safe
implementation.

Go was selected over Rust because its standard library directly covers most of
this daemon, its concurrency and cross-compilation model are straightforward,
and it minimizes build and dependency complexity. Rust's additional memory
safety control is valuable but does not presently justify its larger
implementation burden for this small trusted protocol. Ruby and Bash remain
useful for operator tooling, but requiring their runtimes would weaken portable
single-binary distribution.

The core prefers pure Go and the standard library. A focused dependency is
allowed when reimplementing a security/interoperability standard would be less
safe, such as RFC 8785 canonicalization. Optional Linux/macOS scripts use POSIX
`sh` rather than modern Bash features because the Bash versions supplied across
those platforms differ. Windows support does not force shell parity: users can
run the native executable directly, and PowerShell helpers remain YAGNI until a
real setup flow requires one.

Core interfaces avoid Unix-only assumptions. Stdio is the initial local harness
seam; Unix sockets, launchd, systemd, and Windows supervisors are optional
adapters. Releases are built and checked for all three operating-system
families.

---

## D17: One job, narrow core

**Decision:** this tool does one job: exchange authenticated, project-scoped
messages and artifacts between harness sessions, including offline
reconciliation and bounded presence. Harnesses own agent execution and process
lifecycle.

A proposed feature belongs in the core only when reliable send, receive,
replication, authentication, retention, or liveness cannot work without it.
Prefer an existing Go standard-library or operating-system facility over new
code. Prefer a fixed sensible behavior over configuration until two real use
cases require variation. Prefer deletion and direct code over speculative
interfaces, plugin systems, or compatibility layers.

The core is not a scheduler, orchestrator, workflow engine, service manager,
plugin host, dashboard platform, general database, general message bus, or
arbitrary key-value store. Integrations compose with those tools through the
small local interface instead of absorbing their responsibilities.


---

## D18: Harness-specific bridges are separate projects

**Decision:** the core publishes one small, versioned strict JSONL interface on
stdio and a direct CLI. It does not import Claude Code, Codex, Grok, Pi, or
other harness SDKs.

Native harness tools may be necessary for installation, lifecycle hooks,
message rendering, and model wake-up. Each such bridge lives in a separate
project with its own dependencies and release cadence. A bridge translates
between its harness and the core interface; it does not implement replication,
persistence, signing, validation, or work-state semantics.

This keeps harness churn from expanding or destabilizing the relay. MCP is one
possible external bridge, not a plugin system embedded in the core. The core CLI
remains the reference integration and permits use without any bridge.

---

## D19: Developer guide is a versioned release artifact

**Decision:** `DEVELOPER_GUIDE.md` ships with the core and is required for v1.
It is the integration sequence for both external bridge authors and this
project's own future bridge work. `SPEC.md` remains normative; the guide turns
that protocol into an implementable path with schemas, examples, recovery
behavior, and conformance checks.

The guide version follows the public core-interface version. Any reviewed change
to framing, commands, events, errors, lifecycle, or bridge obligations updates
the guide in the same change. A release is incomplete when its implementation
and guide disagree.

The draft guide records settled responsibilities now and leaves exact wire
examples explicitly pending until the interface is approved. This avoids both
undocumented implementation and speculative documentation that later becomes a
false contract.

---

## D20: Public Go documentation is tested documentation

**Decision:** every public Go package and exported identifier has an idiomatic
Go doc comment. Public integration flows are also runnable `Example` functions
in `_test.go` files, making them visible through `go doc`/`pkgsite` and checked
by `go test`.

Comments document contracts, invariants, errors, and non-obvious reasons. They
do not narrate straightforward internal code. The developer guide owns the
cross-package integration sequence; source documentation owns the interface
next to the implementation. A public interface change updates both in the same
reviewed change.

This uses the standard Go toolchain rather than adding a YARD-like generator or
documentation dependency.

---

## D21: PPP and TDD are mandatory implementation gates

**Decision:** every nontrivial implementation runs through Forge. Forge conducts
the Pseudocode Programming Process (PPP) before implementation and TDD after the
human approves the feature spec.

PPP produces one reviewable artifact containing numbered EARS-lite behaviors,
plain-English pseudocode, decisions, and the expected file surface. The
Ponytail minimalism ladder runs before approval. No implementation starts from
an unapproved nontrivial spec.

TDD implements one behavior at a time with strict RED, minimal GREEN, and
REFACTOR. RED tests freeze the contract; the full suite runs after each GREEN;
final verification checks behavior IDs, test coverage, documentation examples,
and spec fidelity. Refactors preserve characterized behavior under a
green-stays-green rule.

Trivial changes still run the PPP gate inline. A trivial observable behavior
change receives a failing check before its fix; a pure documentation change
does not receive an artificial test.

Forge may run only after its repository, worktree, panel, asset, and enforcement
preflight succeeds. A failed preflight blocks a claimed Forge run. A documented
manual-gate variant requires explicit human approval and is never described as
hook-enforced.

---

## D22: Dogfood after the minimal direct-message path

**Decision:** this project begins using the relay for its own coordination as
soon as an acceptance check proves signed direct send, receipt,
acknowledgment, and delivery to a recipient that reconnects after being
offline. Waiting for a feature-complete release would delay the most relevant
operational feedback.

Early dogfooding runs in shadow mode beside an independent fallback channel.
Relay failures and diagnostic delivery IDs are recorded outside the relay so a
broken product cannot hide its own failure evidence or strand the developers.
Every reproducible dogfood defect enters the normal PPP/TDD workflow with a
failing regression check before its fix. Dogfooding supplements automated
verification and does not replace it.

Relay-only coordination requires explicit human approval after evidence covers
same-machine and cross-machine delivery, offline recovery, duplicate delivery,
and sidecar restart. Dogfooding is a way to validate the narrow core, not a
reason to absorb project management or orchestration features.

---

## D23: Structured logs and bounded status metrics; tracing deferred

**Decision:** v1 implements structured JSON Lines logs on `stderr` and a bounded
metrics snapshot through the existing `status` CLI/stdio operation. It does not
implement a telemetry server or dedicated distributed tracing.

Logs cover lifecycle, authentication, validation, delivery, reconciliation,
recovery, and failures. Existing request, event, reconciliation, and delivery
IDs correlate operations across machines. Logs exclude message/artifact
content, keys, credentials, and authentication material. The caller owns log
capture, rotation, shipping, and retention.

Status reports uptime, queue depth, message outcome counters, peer/presence
state, reconciliation outcomes, pending event/artifact counts, transferred bytes,
lease freshness, and recent sanitized health information. Counters are
in-process and bounded; process start time makes restart resets visible. No
message or participant IDs become metric dimensions.

OpenTelemetry, spans, collectors, and exporters are deferred until correlated
logs fail to diagnose a recorded cross-machine problem. This provides high
runtime visibility without making observability another product.

---

## D24: Use Go's concurrency, safety, and test facilities directly

**Decision:** each sidecar is one Go process with a bounded set of long-lived
goroutines. `context.Context` carries cancellation and deadlines. Channels are
limited to small internal handoffs; state remains owned by one goroutine where
possible, with `sync.WaitGroup`, mutexes, and atomics added only when ownership
cannot remain local. The design does not create one goroutine per message.

The core uses Go's standard Ed25519, SHA-256, secure randomness, TLS/X.509, JSON,
filesystem, path, monotonic-time, error-wrapping, and structured-logging
facilities. Protocol input is size-bounded and decoded into typed structs with
explicit version and schema validation. Distinct identifier types prevent
mixing project, participant, harness, session, and event identities. Immutable
files use exclusive creation or synced same-filesystem temporary files followed
by a platform-verified publication operation. The specification does not assume
that rename has identical atomicity and durability on every supported platform.

Tests use the standard `testing` package, temporary directories, in-memory
connections, HTTP test servers where applicable, runnable examples, fuzzing,
and the race detector. `gofmt`, `go vet ./...`, and `go test ./...` are baseline
checks; `go test -race ./...` runs for concurrency-sensitive changes.

A focused RFC 8785 implementation is the expected external library because
cryptographic canonicalization should not be improvised. `unsafe`, CGO, Go
plugins, reflection-heavy frameworks, speculative generics, unbounded
goroutines, and unnecessary build tags remain outside the initial design.

---

## D25: Two focused Go dependencies at most; notifications deferred

**Decision:** use `github.com/gowebpki/jcs` for RFC 8785 canonicalization. This
is security-sensitive interoperability code with a small raw-JSON interface,
duplicate-key rejection, and upstream RFC vectors. Isolate it behind one
internal wrapper and retain local vector tests.

Use `golang.org/x/sync/errgroup` only if the approved sidecar lifecycle has
multiple error-returning goroutines requiring shared cancellation and error
propagation. Select the release after choosing the minimum Go version. This is a
conditional dependency, not permission to build a generic supervisor.

Use standard `testing/synctest` for deterministic lease, timeout, retry, and
heartbeat tests when the minimum Go version supports it. Run the official
`govulncheck` command during dependency and release verification; it is not a
runtime import.

Defer `fsnotify`. Its cross-platform notifications are non-recursive, do not
cover NFS/SMB, and cannot replace authoritative reconciliation scans. It may be
added only after measured scan cost or delivery latency justifies maintaining a
second path.

The standard library remains sufficient for flags, JSON configuration,
structured logging, cryptography, TLS, IDs, bounded retry/rate logic,
filesystem publication, HTTP, and tests. Do not initially add CLI/config,
logging, assertion, UUID/ULID, database, CRDT, peer-to-peer framework, telemetry,
file-lock, retry, dependency-injection, or plugin libraries.

Syncthing is not imported as a Go library. If later selected for replication,
it remains an external single-purpose executable with an independently managed
lifecycle.

---

## D26: Prior-art alignment fixes the event, delivery, control, and wire contracts

**Decision:** v1 uses immutable RFC 8785-canonical event envelopes in one signed,
gap-free, previous-hash-linked chain per participant. Replicas exchange the
highest contiguous sequence for each author and request missing ranges. An
identical `{author, sequence}` is a duplicate; a different event at that position
is a quarantined fork. HLC supplies only a physical-first deterministic display
view.

Artifacts use the OCI descriptor minimum: SHA-256 digest, byte size, and media
type. The digest covers raw bytes. Receivers check size before hashing. V1 keeps
eager whole-artifact copies and explicit purge; chunking, compression, automatic
GC, and algorithm agility remain deferred.

Session delivery stages a complete directory before publication and provides
at-least-once presentation through ready, processing, done, and failed states.
Claims expire and can redeliver. Acknowledgment proves harness acceptance, not
work completion. Staging and final paths share one filesystem, while platform
tests define the actual durability guarantee.

Each project has one administrator in v1, avoiding conflicting offline control
histories without a consensus rule. The operator keeps an offline recovery copy;
losing every copy blocks later control changes but not ordinary current-epoch
messaging or history reads. Administrator control events change membership,
replace keys, rotate the administrator, close, or reopen the project
and advance its epoch exactly once. After a replica learns the new epoch,
old-epoch events remain stale history but are not delivered or executed. A
partitioned replica can act before learning a close or revocation; v1 provides
no global consensus barrier. Closed replicas keep bounded reconciliation so
they can learn a valid reopen.

The local public seam is strict bounded JSONL with initialization, protocol
version, correlation IDs, one terminal response per request, advisory
cancellation, protocol-only `stdout`, and log-only `stderr`. Machine peers use
mutually authenticated TLS 1.3 with configured trust and addresses. TLS does not
replace project authorization, replay, chain, signature, or artifact checks.

**Library impact:** none beyond D25. Secure Scuttlebutt, Syncthing BEP, OCI,
Maildir, Git quarantine, SQLite, Postfix, Beanstalkd, NATS, AWS idempotency,
Chubby, TUF, LSP, A2A, FoundationDB, and the four harness APIs are design and
test references, not new core dependencies. Syncthing remains an optional
independently operated adapter, not a child process.

This decision supersedes the global-cursor/global-merge language in D2, D4, and
the old summary table while preserving their rejection of consensus and broker
machinery. The supporting review is `PRIOR_ART.md`.

---

## D27: Installation supports integration-led and standalone paths

**Decision:** the core release owns one standalone, agent-readable installation
contract. An operator can install, configure, verify, upgrade, and remove the
core before installing a harness integration. A later integration first detects
and reuses a compatible core installation.

An integration can let its harness agent perform the mechanical setup after no
more than five explicit operator actions in the normal supported case. The
operator still approves the binary source and version, privilege or persistence
changes, project trust, initial peers, and wake policy. Verification failure
stops setup. Private keys, credentials, and transport secrets never enter model
context or logs.

Bridge projects do not fork the installer, silently replace incompatible core
versions, or add core harness-specific setup logic. Unsupported environments
fall back to the standalone guide. Exact commands remain deferred until release
artifacts and the public CLI exist; inventing them now would create an untestable
second interface.

---

## Summary Table — Technologies / Systems That Informed This Design

| Inspiration | What was borrowed | What was deliberately left out |
|---|---|---|
| Secure Scuttlebutt | Signed per-author sequences, previous hashes, gap/fork detection, per-author reconciliation cursors | Social graph, invites, private boxes, custom RPC and canonical JSON |
| Syncthing BEP | Delta cursor reset identity, ordered range exchange, TLS fingerprint trust | Folder version-vector machinery, block progress, imported packages |
| RFC 8785 and OCI | Canonical signed JSON; digest, size, and media type artifact descriptors | Custom canonicalizer, alternate hashes, embedded artifacts |
| Maildir, Git quarantine, SQLite | Hidden staging, validation before publication, explicit durability boundary | Exactly-once claims, shared locks, database journals, Git hook system |
| Postfix, Beanstalkd, NATS, AWS | Bounded active work, claims, acknowledgments, expiry, redelivery, stable request IDs | Broker/server dependencies and automatic work reassignment |
| Chubby and TUF | Lease grace, epoch freshness, trusted key replacement | Consensus locks, fencing service, threshold repository roles |
| LSP, Pi RPC, Codex, A2A | Bounded JSON framing, initialization, correlation, terminal responses, task-status vocabulary | Full JSON-RPC/LSP/A2A surfaces, discovery, webhooks, gRPC |
| FoundationDB | Crash, partition, delay, full-disk, and stale-revival failure matrix | A custom simulator runtime |
| Apache Kafka | Claim-check idea and pull consumption | Global partition order, controllers, broker source of truth |
| OpenViking | Inspectable filesystem-native agent state | Tiered content loading and vector/RAG integration |
| Claude Code, Codex, Grok, Pi | Separate bridge seams and lifecycle hooks/APIs | Harness SDKs and universal bridge abstraction in core |
| Distributed ledgers / blockchains | Tamper evidence through per-author hash chains | Consensus, mining, staking, voting, and one global chain |

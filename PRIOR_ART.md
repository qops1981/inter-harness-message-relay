# Inter-Harness Message Relay — Prior-Art Review

Date: 2026-09-14

Status: research input, not a normative specification. The approved
2026-09-14/15 alignment is recorded in `SPEC.md`, `DESIGN.md`, and
`DECISIONS.md` D26.

## Method

The review divided the relay into 15 mechanisms: identity and key lifecycle;
signed event encoding; offline reconciliation; encrypted peer transport;
crash-safe publication; per-session delivery; artifact replication; identity
separation; presence; waits and backpressure; work status and retry; project
lifecycle; stdio framing; observability; and portable deterministic testing.

A Pi adaptation of the installed Ideate and Refute protocols used isolated
Claude, Gemini, and Codex sessions. Two Ideate rounds used academic and
practitioner lenses. One empirical search was run for each candidate query.
Two independent refuters graded page-one yield and cross-examined each other.
A small rescue pass targeted confirmed gaps. Search refinement then stopped,
after two Ideate→Refute alternations, as required.

The local research archive contains the query records, panel transcripts, and
source snapshots used for this review. It is excluded from the public repository
because it includes third-party page copies and internal model logs. The source
citations and distilled findings required to audit the design remain in this
file.

The initial reference harness set is **Claude Code, OpenAI Codex, Grok, and
Pi**, but it is not a closed compatibility list. Harness code remains outside
the core.

## Executive findings

1. **Secure Scuttlebutt supplies the closest event-history shape.** Use one
   signed, monotonically sequenced, previous-hash-linked feed per author. Do
   not import its social-network, invite, private-message, or custom RPC stack.
2. **Syncthing validates per-device-per-folder delta cursors and reset
   identities.** Its cursor is `{index identity, sequence}`, not one global
   maximum; the relay adapts that rule to one cursor per author feed. Its block
   protocol is unnecessary for initially small artifacts.
3. **Maildir, Git quarantine, and SQLite converge on staging before
   publication.** Their flush guarantees differ: original Maildir specifies
   rename visibility, Git quarantine specifies reachability, and SQLite and
   Dovecot add explicit durability flushes. The relay must define and test its
   own same-filesystem flush-and-publish contract per target platform.
4. **Maildir rename is not exactly-once work execution.** Beanstalkd, NATS, and
   AWS all converge on explicit claim/ack state, claim expiry or redelivery,
   stable request IDs, and idempotent handling.
5. **OCI gives the minimal artifact descriptor:** digest, byte size, and media
   type. Verify the cheap size bound before hashing. OSTree confirms that
   timestamps and stored compression must not change identity; the relay goes
   further and hashes raw artifact bytes without filesystem metadata.
6. **Noise is good protocol prior art, but Go TLS 1.3 remains the lazy v1
   choice.** A correct Noise handshake still needs pattern selection, framing,
   rekeying, and truncation handling. TLS uses mature standard-library tooling
   but also needs X.509 peer provisioning. It covers transport only; project
   authorization, event replay, revocation, and epoch freshness remain
   application rules.
7. **Chubby validates TTL presence semantics, not a consensus requirement.**
   Presence needs a grace interval before the draft's `unknown` state; it must
   not imply that work is progressing. Paxos, locks, and fencing services do
   not belong in this relay.
8. **LSP, Codex, and Pi show that JSON stdio needs precise framing and
   correlation.** Both Content-Length framing and strict LF-delimited JSON are
   proven. The relay should choose one in PPP, bound records before decode, keep
   protocol output off `stderr`, and require terminal responses for accepted
   requests.
9. **A2A is useful task vocabulary, not the replication layer.** Borrow
   request-linked states, ordered updates, terminal-state rules, cancellation
   semantics, and version rejection. Reject Agent Cards, discovery, REST,
   gRPC, SSE, and webhooks for the core.
10. **FoundationDB's lesson is a failure matrix, not a simulator framework.**
    Test crashes between write/flush/publish, delayed and reordered transport,
    partitions, reconnects, full disks, and revived stale peers. Use ordinary
    Go tests and `testing/synctest` only when the selected Go baseline supports
    it.
11. **The four harnesses have different thin seams.** Claude Code documents
    lifecycle hooks; Grok claims Claude Code hook compatibility but its full
    hook contract was not reviewed; Codex exposes an app-server protocol; Pi
    exposes extensions and an existing strict JSONL RPC mode. One universal
    bridge implementation would be false reuse. They should share only the
    relay's versioned contract and conformance examples.
12. **The aligned documents correct the earlier contradictions.** They replace
    the global-highest-ID reconciliation, reversed HLC tuple, direct publication
    into `new`, ambiguous `new→cur`, single project-log order, and optional hash
    chains with the D26 event, delivery, control, and wire contracts.

## Component-to-prior-art map

| Relay mechanism | Best prior art | Keep | Leave out |
|---|---|---|---|
| Identity and key lifecycle | SSB; TUF | Separate signing identity; versioned trusted key replacement and explicit old-key rejection | TUF role hierarchy and threshold workflow in v1 |
| Identity separation | SSB feed keys; Syncthing device/folder IDs; harness session APIs | Distinct machine, harness, session, participant, project, and event identifiers | One overloaded name or raw display value used as an ID/path |
| Signed immutable events | Secure Scuttlebutt; RFC 8785 | Per-author sequence, previous hash, signature, event content hash; JCS | SSB's private boxes, metafeeds, invite/pub system, custom canonical JSON |
| Reconciliation | SSB EBT; Syncthing BEP | Map of author/index identity to contiguous sequence; reset identity after rebuild | Bit-packed EBT clocks, one global cursor, block-progress protocol |
| Encrypted transport | TLS 1.3; Noise | Mutual peer authentication, fresh sessions, bounded frames, explicit close/errors | Custom Noise implementation before TLS fails a measured need; ICE stack |
| Crash-safe publication | Maildir; Git quarantine; SQLite | Unpublished staging, flush, validate, atomic publication, recovery scan | Shared NFS locks, rollback journals, Git hook machinery |
| Session delivery | Maildir; Beanstalkd; NATS | Immutable item, ready/claimed/done-or-failed, bounded blocking wait, explicit ack | Exactly-once promise, broker/server dependency, unbounded pulls |
| Artifact copies | OCI descriptors; OSTree; Git | `sha256:<lower-hex>`, size, media type, immutable logical bytes | Chunking, packfiles, compression, GC, algorithm agility in v1 |
| Presence | Chubby leases | TTL, grace before `unknown`, monotonic local expiry, bounded heartbeat | Consensus lock service, fencing tokens for ordinary messages |
| Waits and backpressure | NATS pull consumers; Postfix; Beanstalkd | Bounded wait, batch, bytes, active claims, retry delay, and normal empty timeout | Unbounded queues, permanent blocking, broker dependency |
| Work and retries | AWS idempotency; Beanstalkd; A2A | Stable request ID, payload-match check, ordered status events, terminal states | Automatic reassignment of possibly non-idempotent work |
| Project lifecycle | Syncthing index reset; TUF versions/expiry | Prior art suggests explicit epoch/version changes and stale-control rejection; PPP must define them | TUF's four-role repository and threshold workflow in v1 |
| Stdio/JSON | Pi RPC; Codex app-server; LSP | Explicit version/init, correlation IDs, bounded framing, stdout purity | Full JSON-RPC/LSP feature surfaces |
| Observability | W3C Trace Context | Opaque random correlation IDs and pass-through rules; external prior art for bounded logs/metrics was not reviewed | `tracestate`, tracing SDK, exporter, collector, metric label IDs |
| Failure testing | FoundationDB; Go `testing/synctest` | Deterministic clocks/schedules where possible; fault matrix; fuzzing remains an explicit research gap | A custom distributed simulator |

## Project and specification reviews

### Secure Scuttlebutt

**Source fact.** An identity owns one append-only feed. Each message contains a
sequence, the previous message ID, author key, timestamp, content, and signature.
The message ID is the SHA-256 hash of the signed canonical bytes. History streams
request one feed from a sequence; EBT exchanges a vector clock keyed by feed.
Blobs are also SHA-256 addressed and its want/have mechanism bounds propagation.

**Solved gotchas.** Feed forks are detectable because both successors name the
same previous event. Replication has a simple history-stream fallback when EBT
negotiation fails. Blob requests include size limits. Key loss creates a new
identity rather than silently continuing the old feed.

**Relay conclusion.** Adopt the feed invariant and simple sequence-map
anti-entropy. Keep plain fields instead of EBT bit packing. Use RFC 8785 rather
than SSB's runtime-specific canonical form. SSB proves forks are detectable;
the proposed quarantine-and-report response is a relay design decision because
signatures cannot decide which branch is authorized.

Source: [Scuttlebutt Protocol Guide](https://ssbc.github.io/scuttlebutt-protocol-guide/).

### Syncthing Block Exchange Protocol

**Source fact.** A folder index has a random `index_id` and device-local
monotonic sequences. `{index_id, max_sequence}` identifies a delta point. A
reset index must get a new ID. Announced deltas are sequence ordered. File
conflicts use version vectors. Transport is TLS 1.3 or later and authenticates
configured device certificate fingerprints.

**Solved gotchas.** A new index ID prevents peers from mistaking a rebuilt index
for continuation of the old sequence. Receivers must accept valid non-default
block sizes. Version changes invalidate earlier download-progress claims.

**Relay conclusion.** Copy index-reset and ordered-delta rules for per-author
feeds and project epochs. Do not copy file version vectors: the relay prevents
multi-writer author feeds instead of resolving them. Whole-artifact copies are
simpler than block exchange until measurements show otherwise. Syncthing can
remain an optional independently managed replication executable, never a child
or imported core library.

Source: [Syncthing BEP v1](https://github.com/syncthing/docs/blob/main/specs/bep-v1.rst).

### RFC 8785 JSON Canonicalization Scheme

**Source fact.** JCS requires I-JSON, rejects duplicate keys and invalid Unicode,
emits UTF-8 without insignificant whitespace, serializes numbers by the defined
ECMAScript rule, and recursively sorts property names by UTF-16 code units.
NaN, infinity, and lone surrogates are errors.

**Solved gotchas.** It removes cross-runtime differences in key order, number
format, escapes, Unicode, and whitespace before signing.

**Relay conclusion.** Keep D25's small reviewed JCS dependency and upstream test
vectors. Restrict signed numeric schema fields to bounded integers and schema
property names to ASCII. Do not write a partial home-grown canonicalizer.

Source: [RFC 8785](https://www.rfc-editor.org/rfc/rfc8785).

### OCI descriptors, OSTree, and Git objects

**Source fact.** OCI's minimal descriptor requires `mediaType`, `digest`, and
`size`; SHA-256 is mandatory and lowercase hex is canonical. It recommends size
verification before expensive processing. OSTree hashes logical content,
deliberately excludes timestamps, and documents cases where stored compressed
bytes do not match the named logical-content hash. Git receives objects into a
quarantine namespace, validates them before migration, and prevents refs from
exposing quarantined objects. Git GC uses grace periods and recent-object
protection but explicitly admits concurrent pruning is not a complete safety
solution.

**Solved gotchas.** Quarantine prevents rejected input from becoming reachable
or filling the permanent object store. Logical-content hashing avoids identity
changes caused by download time or storage compression. Git's warning proves
that mtime alone is not a correctness proof for concurrent deletion.

**Relay conclusion.** Store whole immutable artifacts by SHA-256 with size and
media type. Hash raw artifact bytes; OSTree's broader content objects also
include selected filesystem metadata, so only its timestamp/storage-form lessons
carry over. Validate in staging before publication. V1 already prefers storage
and explicit project purge, so defer automatic GC, packfiles, chunking,
compression, embedded data, alternate hash algorithms, and summary indexes.

Sources: [OCI descriptor](https://github.com/opencontainers/image-spec/blob/main/descriptor.md),
[OSTree repository](https://ostreedev.github.io/ostree/repo/),
[git-receive-pack quarantine](https://git-scm.com/docs/git-receive-pack), and
[git-gc concurrent-prune warning](https://git-scm.com/docs/git-gc).

### Maildir, Dovecot, SQLite, and Postfix

**Source fact.** Maildir writes a unique file under `tmp` and moves the complete
file to `new`. Dovecot describes one immutable message per file and calls
`fsync`/`fdatasync` where needed to avoid acknowledging lost mail. SQLite's
rollback-journal design flushes recovery data before database writes and makes
journal deletion the commit point; its documentation explicitly depends on
honest flush, locking, and delete behavior. Postfix places one message in a
queue file and uses a bounded active queue plus a separate deferred queue.

**Solved gotchas.** Staging hides partial writes. Flush-before-commit avoids
acknowledging volatile state. A bounded active window prevents a large durable
backlog from becoming an equally large memory backlog. SQLite warns that NFS
and some Windows network filesystem locks can be subtly broken.

**Relay conclusion.** Stage each complete delivery directory outside `new`,
flush according to the approved durability contract, then publish on the same
filesystem. Keep a bounded active claim set and durable retry/failure state.
Do not claim portable atomic rename stronger than Go and the target filesystem
provide: Go documents that `os.Rename` is not atomic on all non-Unix platforms,
and cross-filesystem moves cannot be the publication primitive. Keep staging
and final paths on one filesystem, test Windows explicitly, and document
supported durability assumptions. Do not add SQLite, file locks, or Postfix's
daemon topology.

Sources: [Maildir](https://cr.yp.to/proto/maildir.html),
[Dovecot mailbox formats](https://doc.dovecot.org/2.3/admin_manual/mailbox_formats/),
[SQLite atomic commit](https://sqlite.org/atomiccommit.html),
[Postfix architecture](https://www.postfix.org/OVERVIEW.html), and
[Go `os`](https://pkg.go.dev/os).

### Beanstalkd, NATS JetStream, and AWS idempotent APIs

**Source fact.** Beanstalk jobs move through ready, reserved, delayed, and
buried states. A time-to-run reservation expires back to ready unless the worker
deletes, releases, or buries the job. `reserve-with-timeout` is a bounded block.
JetStream pull consumers ask for bounded batches with expiration and explicit
acknowledgment; empty fetches are normal and no-expiry raw pulls can stall. AWS
uses caller-provided request IDs, treats same-ID same-intent requests as
duplicates, requires token recording and mutation to share an atomic outcome,
and rejects token reuse with changed parameters.

**Solved gotchas.** Claim expiry recovers work after a worker crash but permits
redelivery. Count and byte bounds prevent slow consumers from exhausting
memory. Stable intent IDs distinguish a retry from a new operation; finite
retention means very late retries cannot always be deduplicated.

**Relay conclusion.** Promise at-least-once delivery attempts, not exactly-once
agent effects. Tie every delivery and work-status transition to immutable event
and request IDs. On duplicate IDs, compare the signed payload hash; never accept
a changed request as the same operation. Keep bounded `wait`/batch/byte values
and make timeout-with-no-message a normal result. Do not run NATS or beanstalkd
inside the core.

Sources: [beanstalkd protocol](https://github.com/beanstalkd/beanstalkd/blob/master/doc/protocol.txt),
[NATS pull consumers](https://docs.nats.io/nats-concepts/jetstream/consumers), and
[AWS idempotent APIs](https://aws.amazon.com/builders-library/making-retries-safe-with-idempotent-APIs/).

### Noise, TLS, ICE, and Bundle Protocol

**Source fact.** Noise specifies composable authenticated DH handshakes and
requires fresh ephemeral keys; its transport messages have explicit nonce and
message-size rules. ICE gathers and checks address candidates through STUN/TURN
and limits checks partly to control amplification. Bundle Protocol is a full
delay-tolerant store-and-forward system with lifetimes, fragmentation, routing,
administrative records, and CBOR encoding.

**Solved gotchas.** Noise calls out nonce exhaustion, out-of-order nonce
management, key reuse, and truncation/termination concerns. ICE handles NAT
role conflict and candidate selection but adds servers and an attack surface.
Bundle lifetimes bound indefinitely retained traffic, but inaccurate clocks and
fragments complicate expiration.

**Relay conclusion.** Use Go TLS 1.3 with mutual configured peer trust unless a
measured deployment proves it insufficient. Do not implement Noise merely to
avoid X.509: the relay still needs key provisioning and lifecycle rules. Keep
ICE outside core; operators can supply reachable addresses, relays, or a VPN.
Do not import Bundle Protocol. Its one useful reminder is already covered:
acknowledge only durable acceptance and bound retention explicitly. Its
fragmentation machinery is also rejected with artifact chunking until measured
whole-artifact transfer limits justify both.

Sources: [Noise specification](https://noiseprotocol.org/noise.html),
[RFC 8445 ICE](https://www.rfc-editor.org/rfc/rfc8445), and
[RFC 9171 Bundle Protocol](https://www.rfc-editor.org/rfc/rfc9171).

### Chubby and TUF

**Source fact.** Chubby sessions renew through keepalives and enter a grace or
"jeopardy" interval before expiry; its lock sequencers let protected resources
reject stale lock holders. TUF uses signed versioned expiring metadata,
threshold roles, consistent hashed names, and explicit defenses against
rollback, freeze, mix-and-match, and fast-forward attacks.

**Solved gotchas.** A grace state avoids immediately turning delayed heartbeats
into definitive death. Chubby observed keepalive storms. TUF demonstrates that
a valid signature on old metadata is not proof of freshness and that key
replacement needs a trusted transition.

**Relay conclusion.** Presence has live, uncertain, and expired/unknown states,
uses monotonic local deadlines, adds jitter, and never proves work progress.
Membership and key rotation require versioned signed control events and explicit
old-key rejection. Do not copy Chubby consensus/locks or TUF's repository roles
and threshold workflow into v1. Admin-key authorization still needs a normative
PPP decision; Ed25519 signatures alone prove identity, not permission.

Sources: [Chubby paper](https://research.google.com/archive/chubby-osdi06.pdf)
and [TUF specification](https://theupdateframework.github.io/specification/latest/).

### LSP, Pi RPC, Codex app-server, and A2A

**Source fact.** LSP uses required byte-counted `Content-Length` framing, an
initialize gate, request IDs, notifications without responses, advisory
cancellation, and mandatory terminal responses even after cancellation. Pi RPC
uses strict LF-delimited JSON: split only on LF, optionally strip CR, and do not
use readers that also split Unicode separators. It supplies optional request
IDs, a stable session ID, append-tree cursors, queued steer/follow-up modes,
and an `agent_settled` event. Codex app-server uses bidirectional JSON-RPC-like
messages over JSONL stdio by default (while omitting the standard `jsonrpc`
member on its wire), requires initialize, rejects overload with
a retryable error, and exposes thread/turn/item lifecycle. A2A separates its
data model from protocol bindings and defines tasks, ordered updates, terminal
states, polling/streaming/push, cancellation, versions, artifacts, and auth.

**Solved gotchas.** Initialization gates prevent use before capabilities and
versions are known. Correlation IDs allow interleaved responses. Cancellation
is not completion and therefore cannot leave requests hanging. Strict framing
keeps logs and incidental output from corrupting stdout. Codex bounds request
queues and tells clients to back off with jitter.

**Relay conclusion.** The smallest likely seam is strict bounded JSONL, because
Pi and Codex already prove it and JSON escapes embedded newlines. PPP must still
choose and freeze framing before implementation. Include initialize/version,
request ID, explicit errors, advisory cancel, and terminal outcome. Put JSONL
logs only on `stderr`. Borrow A2A's task vocabulary, not its discovery, network
bindings, webhook, extension, or Agent Card systems. A2A independently uses JCS
for Agent Card signatures, which corroborates RFC 8785 without making Agent
Cards part of this relay.

Sources: [LSP 3.17](https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/),
[Pi RPC documentation](https://github.com/earendil-works/pi-mono/blob/main/packages/coding-agent/docs/rpc.md),
[Codex app-server](https://developers.openai.com/codex/app-server), and
[A2A specification](https://github.com/a2aproject/A2A/blob/main/docs/specification.md).

### W3C Trace Context

**Source fact.** Trace Context defines opaque versioned random identifiers,
pass-through behavior, size limits, and privacy/security cautions. Identifiers
must not embed user-identifiable data; attacker-controlled sampling and
correlation values can create cost or collision attacks.

**Relay conclusion.** D23 is still correct: use the relay's existing random
request/event/reconciliation/delivery IDs in structured logs. Do not add
`traceparent`, `tracestate`, OpenTelemetry, exporters, or collectors until a
recorded diagnostic failure justifies them. Never encode participant, machine,
project, or message content into a correlation ID.

Source: [W3C Trace Context](https://www.w3.org/TR/trace-context/).

### FoundationDB simulation and Go testing

**Source fact.** FoundationDB runs a deterministic single-threaded simulation
of machines, disks, networks, partitions, delay, full disks, restarts, and
nodes returning from the dead. It separates correctness simulation from
performance testing. Go's `testing/synctest` provides a fake clock and waits for
an isolated goroutine bubble to become durably blocked; external I/O is not
made deterministic and should use fakes such as `net.Pipe`.

**Relay conclusion.** Reuse the failure catalogue, not FoundationDB's Flow
runtime. Every nontrivial durability or reconciliation behavior needs the
smallest deterministic check that crashes at its publication boundary or
reorders its messages. Keep real filesystem integration tests separate from
fake-time concurrency tests. D25's conditional use of standard `synctest`
remains appropriate for the chosen minimum Go version.

Sources: [FoundationDB testing](https://apple.github.io/foundationdb/testing.html)
and [Go synctest](https://go.dev/blog/synctest).

## Harness bridge findings

The bridge findings are implementation leads for separate projects, not core
requirements.

### Claude Code

Official hooks receive JSON on stdin and return structured JSON/exit status.
Useful lifecycle events include `SessionStart`, tool-use hooks, `Stop`, and
`SessionEnd`; `additionalContext` is the supported context-injection field.
Async hook output cannot enforce decisions, hooks have timeouts/output caps,
and `SessionEnd` is too short and unreliable to replace TTL presence.

**Thin bridge:** a command hook invokes the relay CLI or talks to the already
running per-session sidecar. The hook is transient; it must not create another
persistent relay. Project hook trust and path portability are deployment
concerns. Never depend on a successful shutdown hook for correctness.

Source: [Claude Code hooks](https://docs.anthropic.com/en/docs/claude-code/hooks).

### OpenAI Codex

The app-server's stdio JSONL surface exposes initialization,
thread/turn/item events, approvals, item injection, steering, overload errors,
and generated schemas. WebSocket is documented as experimental and has had
unsafe non-loopback defaults during rollout.

**Thin bridge:** translate relay delivery into the stable item-injection or
steering operation supported by the pinned app-server version; translate
approvals only if explicitly requested. Do not mirror Codex thread storage,
process execution, filesystem APIs, apps, or experimental dynamic tools.
Version-pin and conformance-test the generated schema because this surface can
change independently of the core.

Source: [Codex app-server](https://developers.openai.com/codex/app-server).

### Grok

The official xAI page states that Grok plugins can contain hooks for tool and
session lifecycle events and that Grok reads Claude Code hooks and related
configuration. Project hooks require explicit trust. The reviewed page links to,
but does not itself contain, the complete hook JSON contract.

**Thin bridge:** first test the existing Claude Code hook package against Grok's
compatibility mode. Reuse it only after conformance checks prove the exact
needed events, JSON fields, blocking behavior, and timeouts. Otherwise package
the same translation as a Grok plugin. Do not infer unreviewed hook behavior
from the compatibility claim.

Source: [xAI Skills, Plugins & Marketplaces](https://docs.x.ai/build/features/skills-plugins-marketplaces).

### Pi

Pi's **extension API** can start session-scoped resources on `session_start`,
requires idempotent cleanup on `session_shutdown`, and injects custom or user
messages with `sendMessage` and `sendUserMessage`. `sendMessage` distinguishes
`steer`, `followUp`, and `nextTurn`. Extensions run with full user permissions;
project-local extensions load only after trust. Session replacement tears down
the old runtime, and captured old session objects become stale.

Pi's separate **RPC mode** already provides strict JSONL framing, optional
correlation IDs, a stable session ID, explicit steer/follow-up commands, queue
controls, and lifecycle events such as `agent_settled`.

**Thin bridge:** prefer a small Pi extension connected to the one session
sidecar when integrating an interactive Pi session. Start the connection only
at `session_start`, close it at `session_shutdown`, reuse Pi's session UUID, use
extension `nextTurn` for observed traffic that must not wake the model, and use
`steer`/`followUp` only under local wake policy. For a headless Pi process, an
RPC client may be thinner than an extension. Do not parse Pi session files as a
live API when extension/RPC APIs exist.

Sources: installed Pi
[`extensions.md`](https://github.com/earendil-works/pi-mono/blob/main/packages/coding-agent/docs/extensions.md),
[`rpc.md`](https://github.com/earendil-works/pi-mono/blob/main/packages/coding-agent/docs/rpc.md), and
[`session-format.md`](https://github.com/earendil-works/pi-mono/blob/main/packages/coding-agent/docs/session-format.md).

## Confirmed gotcha checklist for future PPP/TDD

1. Reject duplicate JSON keys, invalid Unicode, unsupported versions, unsafe
   integer ranges, unknown authors, bad signatures, wrong project/epoch, chain
   gaps, and forks before publication.
2. A single high-water mark cannot reconcile independent writers. Exchange one
   contiguous sequence per author and request explicit gaps. For an already
   stored `{author, sequence}`, drop identical bytes and treat different bytes
   as a fork.
3. Never publish directly into a visible queue or object path. An error from a
   convenience whole-file write may leave partial bytes.
4. Do not acknowledge before the required durability point. Flush and rename
   behavior varies by filesystem and operating system; staging and publication
   must not cross filesystem mounts.
5. A rename claims bytes, not exactly-once external work. Claims can expire and
   redeliver after crashes.
6. Duplicate request ID plus different payload is an error, not a retry.
7. Bound record bytes before JSON decode; bound body, artifact, batch, pending
   claims, waits, retries, logs, and status snapshots.
8. Keep protocol output and logs on different streams. Treat a clean transport
   close separately from an authenticated application-level terminal outcome.
9. Use monotonic local time for lease expiry. Clock timestamps from peers are
   descriptive, not an authority for local liveness.
10. Session shutdown hooks are hints. Presence expires by lease when a process
    dies, is killed, loses the network, or times out.
11. Full-disk, read-only, permission, symlink/path escape, stale staging,
    partial transfer, corrupted hash, and concurrently arriving duplicate cases
    need runnable regression checks.
12. Do not delete content concurrently in v1. Explicit project purge is smaller
    and safer than automatic GC; add GC only after a measured storage problem
    and a proven reachability/grace protocol.

## Research gaps and non-findings

- Search engines poorly indexed the official SSB, Syncthing BEP, crash
  consistency, OSTree, FoundationDB, Go `synctest`, and Pi pages. Known official
  URLs were fetched directly; zero search yield was not treated as lack of prior
  art.
- The reviewed Grok page is sufficient to establish hooks, plugins, project
  trust, and stated Claude Code compatibility, but not the complete hook wire
  contract. A bridge must review and pin that contract when implemented.
- The source set did not prove one portable filesystem publication sequence with
  identical crash guarantees on Linux, macOS, Windows, NFS, and SMB. The
  implementation specification must state supported assumptions and test each
  release target; it must not overclaim.
- Neither signatures nor authenticated TLS alone define project authorization,
  administrator powers, key revocation, or old-epoch write policy. D26 now
  defines one v1 administrator, epoch-advancing control events, and stale
  non-actionable old-epoch history.
- Prior art supports at-least-once delivery plus idempotency; it does not make
  arbitrary agent actions exactly once.
- No dedicated primary source was reviewed for Go fuzzing, structured-log
  discipline, bounded status metrics, or recovery diagnostics. Their existing
  draft requirements remain plausible but lack this review's external
  validation.
- No evidence justified ICE, libp2p, a broker, database, consensus service,
  tracing stack, automatic GC, block chunking, compression, or a universal
  harness SDK abstraction in v1.

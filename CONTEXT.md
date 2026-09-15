# Inter-Harness Message Relay

This context describes authenticated project communication between agent
harness sessions. It distinguishes durable project history, local delivery, and
ephemeral liveness.

## Language

### Identity and scope

**Operator**:
The human who approves installation, local privilege changes, project trust, and
peer connections.
_Avoid_: Participant, agent, project administrator

**Project**:
The membership, replication, history, retention, and lifecycle scope for related
agent work.
_Avoid_: Workspace, channel, room

**Machine**:
A physical or virtual host that stores project replicas and can run multiple
harness sessions.
_Avoid_: Participant, agent, peer

**Harness**:
A configured agent runtime installation, such as Claude Code, Codex, Grok, or Pi, that can own
multiple sessions.
_Avoid_: Agent, sidecar, bridge

**Session**:
One resumable harness execution context with isolated delivery state.
_Avoid_: Process, participant, harness

**Participant**:
A stable cryptographic agent identity assigned to one session at a time.
_Avoid_: Session, machine, display name

**Bridge**:
A separate harness-specific translator between native harness operations and the
core interface.
_Avoid_: Plugin, sidecar, core adapter

**Sidecar**:
One core relay process dedicated to one harness session.
_Avoid_: Machine daemon, bridge, peer

### Durable history

**Project history**:
The accepted durable event set for one project.
_Avoid_: Global log, session queue, presence state

**Event**:
An immutable, signed record in project history.
_Avoid_: Mutable row, presence signal

**Author chain**:
The gap-free sequence of events signed for one participant, with each event
naming its predecessor.
_Avoid_: Project history, display order, reconciliation cursor

**Fork**:
Two different events that claim the same participant and author sequence.
_Avoid_: Concurrent events from different participants, session branch

**Stale event**:
A valid event authored for an older project epoch. It remains history but cannot
change current state after the newer epoch is known.
_Avoid_: Invalid event, expired presence, duplicate event

**Message**:
An event that communicates text or an artifact reference to participants.
_Avoid_: Event, notification, presence

**Work status**:
An event that reports accepted, progress, completed, or failed work for a linked
request.
_Avoid_: Heartbeat, presence

**Artifact**:
Content-addressed bytes referenced by an event and replicated with the project.
_Avoid_: Message body, attachment row, blob

**Artifact descriptor**:
The SHA-256 digest, byte size, and media type that identify and bound an
artifact.
_Avoid_: Artifact bytes, download URL, filename

**Project administrator**:
The one v1 participant identity authorized to change membership, keys, and the
project lifecycle.
_Avoid_: Orchestrator role, machine owner, transport peer

**Control event**:
An event from the project administrator that advances the project epoch and
changes membership, key trust, or lifecycle state.
_Avoid_: Message, local configuration edit, presence signal

**Project epoch**:
The lifecycle generation against which an event is authored. A control event
advances the project epoch.
_Avoid_: Schema version, timestamp, author sequence

**Event ID**:
The SHA-256 digest of one canonical signed event envelope.
_Avoid_: Author sequence, request ID, delivery ID

### Replication

**Replica**:
One machine's complete usable copy of a project's durable events and artifacts.
_Avoid_: Backup, queue, peer

**Peer**:
An authenticated remote holder from which a replica exchanges project data.
_Avoid_: Participant, session, agent

**Author sequence**:
The gap-free position of an event in one participant's signed event chain.
_Avoid_: Global order, ULID, timestamp

**Reconciliation cursor**:
The highest contiguous author sequence a replica has accepted for each
participant.
_Avoid_: Session cursor, message ID

### Session delivery

**Delivery**:
A session-local presentation of a durable event.
_Avoid_: Replication, message, injection

**Delivery relation**:
The session-local classification direct, broadcast, or observed.
_Avoid_: Recipient, awareness mode

**Awareness mode**:
The session choice to file addressed traffic or all project traffic.
_Avoid_: Authorization, subscription

**Queue**:
Session-local delivery state derived from project history. Deleting queue state
can cause safe redelivery but cannot remove project history.
_Avoid_: Project history, message bus

**Delivery claim**:
A time-bounded session-local record that one delivery is being presented to its
harness. An expired claim returns to ready state.
_Avoid_: Work acceptance, session lease, acknowledgment

**Acknowledgment**:
A session-local record that the harness accepted one delivery. It does not prove
that an agent performed the requested work.
_Avoid_: Work completion, event signature, replication receipt

**Session cursor**:
The point through project history that one session has examined for filing.
_Avoid_: Reconciliation cursor, acknowledgment

### Liveness

**Session lease**:
A renewable, time-bounded indication that a sidecar still sees its harness
session.
_Avoid_: Work progress, durable event

**Presence signal**:
Signed, replaceable, TTL-bound session state exchanged outside durable project
history.
_Avoid_: Heartbeat event, work status

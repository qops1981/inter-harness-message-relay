# S01 — Signed local message

- Task: `IHR-S01-PPP`
- Feature: `S01-signed-local-message`
- Status: **CLEARED 2026-09-23**
- State: `.forge/state.json`
- Worktree: `/mnt/nfs/energizer/code/ai/worktrees/ihr-s01-signed-local-message`
- Base SHA: `a4ba85c26840a8722d5af908863c9c8f4f9be654`
- Mode / venue / tier: `interactive` / `subagent` / `feature`
- Baseline failures / flagged tests: `[]` / `[internal/relay/relay_test.go]`
- Roster assessment: `2026-09-23T14:10:30Z`
- Design assignment: `openai-codex/gpt-5.6-sol high`
- Forge state on entry: `design`, unchanged by PPP.
- Approval: Human approved with `Approve` at 2026-09-17T19:56:54.945Z.
- Frozen-test amendment approval: Human approved at 2026-09-17T20:53:12.324Z.
- Second frozen-test amendment approval: Human approved at 2026-09-17T21:26:06.609Z.
- Standing continuation: Human authorized persistent S01-only `Yes` / `Continue` on 2026-09-20T18:32:46.078Z; commit, push, PR, deployment, and merge remain separately gated.
- Verification: B1–B25, B25 subprocess proof, race, vet, fuzz, cross-build, module verification, and `govulncheck` passed on 2026-09-23.
- Refute findings resolved: JCS grammar/whitespace handling, canonical-envelope response preservation, and non-EOF input-fault classification.
- Fresh-eyes findings resolved: managed-entry/root checks, 4096-byte marker limit, cleanup warnings, response accounting, status start bounds, approved file surface, setup conflict classification, formatting, documentation, and primary example.
- Final panel: correctness and execution found no remaining issue after correction; the security lens found no consequential issue.
- Delivery: PR [#1](https://github.com/qops1981/inter-harness-message-relay/pull/1) merged as `5c52428ca408fe9b9fce7680852a84898056cc2f`; the human accepted the evidence by directing merge.

This specification covers only the first uncleared sprint. It defines an S01
profile of interface version 1. It does not implement the complete v1 protocol.

## 1. Behaviors

Each behavior maps to one Go test. A test can use table cases for the exact
contract that its behavior names. `[public]` marks CLI or stdio behavior.

B1. [public] WHEN initialization makes a project, the relay MUST publish all durable state below the selected hidden root without an event.
B2. [public] WHEN initialization opens correct project state, the relay MUST use the same local identity and trust.
B3. [public] WHEN a scope identifier does not obey path rules, the relay MUST reject it before filesystem access.
B4. [public] WHEN initialization finds an existing-state error, the relay MUST give the specified initialization error without changes.
B5. [public] WHEN input has correct framing, the relay MUST accept LF, CRLF, and a 131072-byte record.
B6. [public] WHEN input does not obey a framing rule, the relay MUST stop with the specified framing error.
B7. [public] WHEN JSON or its schema is incorrect, the relay MUST reject the record before an operation.
B8. [public] WHEN a request does not obey protocol-state rules, the relay MUST give the specified protocol-state error.
B9. [public] WHEN the relay accepts a completed request, the relay MUST write one bounded terminal response with its request ID.
B10. [public] WHEN a connection meets a lifecycle condition, the relay MUST obey the specified connection result.
B11. [public] WHEN the operator uses the direct CLI, the relay MUST obey the same operation and error contracts.
B12. [public] WHEN a message does not obey recipient or body rules, the relay MUST reject the message without publication.
B13. WHEN the JCS wrapper receives a local vector, the wrapper MUST give the specified canonical result.
B14. WHEN the relay makes a message, the relay MUST make the specified signed envelope and event ID.
B15. WHEN the author chain is empty, the relay MUST use sequence 1, null predecessor, and logical value 0.
B16. WHEN the relay appends after restart, the relay MUST continue the verified sequence, predecessor, and HLC.
B17. WHEN an event integer cannot increase, the relay MUST give `integer_limit` without publication.
B18. WHEN publication meets an error or collision condition, the publisher MUST obey the publication table.
B19. [public] WHEN history reads verified project history, the relay MUST give the specified zero-or-one-event page.
B20. [public] WHEN project history is incorrect, the relay MUST reject each history-dependent operation without publication or event content.
B21. [public] WHEN stdout fails after publication, the relay MUST keep the event for subsequent history.
B22. [public] WHEN status completes, the relay MUST give the specified bounded process snapshot.
B23. [public] WHEN the relay writes output, the relay MUST obey the separation and sanitation contract.
B24. [public] WHEN context cancellation occurs before publication, the relay MUST give `canceled` without publication.
B25. [public] WHEN the S01 integration proof runs, the relay MUST complete all proof steps in section 7.

## 2. Plain-English pseudocode

A failed step returns its classified error unless its function gives an explicit error branch. Each function closes
its owned file handles on every return. The tables in section 3 supply schemas,
limits, error precedence, and operation results.

```text
function run(arguments, input, output, logs, context) -> exit code
  Open the JSON logger on stderr.
  Initialize the process snapshot.
  Write the startup log.
  Validate the startup arguments.
  IF argument validation gives an error,
    Write one terminal error and one sanitized log.
    IF the output fails,
      Set the operation exit code to 1.
    ELSE,
      Set the operation exit code to 2.
  ELSE,
    IF the command is stdio,
      Get the connection exit code.
    ELSE,
      Get the direct-command exit code.
  Write the shutdown log.
  RETURN the operation exit code.

function read record(input) -> record, EOF, or framing error
  Read through LF with a 131073-byte detection limit.
  IF bytes before LF are more than 131072,
    RETURN record_too_large.
  ELSE,
    Continue.
  IF EOF follows zero bytes,
    RETURN EOF.
  ELSE,
    Continue.
  IF EOF occurs before LF,
    RETURN truncated_record.
  ELSE,
    Remove LF.
  IF CR is the last record byte,
    Remove one CR.
  ELSE,
    Continue.
  RETURN the record.

function decode request(record, connection state) -> typed request or error
  Validate JSON grammar, Unicode, numeric tokens, nesting depth, and member counts.
  Canonicalize the record with the JCS wrapper.
  Validate specified field names, field types, mandatory fields, and permitted null values.
  Decode the typed request.
  Validate the operation schema and protocol state in the specified sequence.
  RETURN the typed request.

function operate connection(input, output, context) -> exit code
  Set the connection state to uninitialized.
  WHILE the connection state is not stopped,
    Read one record.
    IF the result is clean EOF,
      RETURN exit code 0.
    ELSE,
      Continue.
    IF the result is a framing error,
      Write one terminal error with null request ID.
      IF the output fails,
        RETURN exit code 1.
      ELSE,
        RETURN exit code 2.
    ELSE,
      Continue.
    Decode the request.
    IF request decoding gives an error,
      Write one terminal request error.
    ELSE,
      Do the request operation.
      Write one terminal operation response.
    IF the output fails,
      RETURN exit code 1.
    ELSE,
      Continue.
    IF shutdown completes,
      Set the connection state to stopped.
    ELSE,
      Continue.
  RETURN exit code 0.

function operate direct command(arguments, output, logs, context) -> exit code
  Decode the command arguments with the same schemas and limits.
  IF the command is initialize,
    Do initialization with an uninitialized connection.
  ELSE,
    Open and validate the project.
    Do the selected version 1 operation.
  Write one terminal response and one sanitized log.
  IF the output fails,
    RETURN exit code 1.
  ELSE,
    RETURN the exit code for the classified result.

function initialize(scope, versions, create) -> initialization result or error
  Validate the version offer, scope, and connection state.
  IF create exists,
    Make the project directory with exclusive creation.
    Make the local Ed25519 seed.
    Make the two-participant trust document.
    Publish the seed.
    Publish project.json last.
  ELSE,
    Read bounded project.json and seed files.
  Validate the scope, file types, specified trust schema, and derived public key.
  Set the connection state to initialized.
  RETURN the initialization result.

function send(project, recipient, body, context) -> event ID or error
  Validate project trust, recipient, and body.
  Validate context cancellation.
  Scan and validate the full author chain.
  Calculate the next author sequence and HLC in the integer limits.
  Make the specified message payload.
  Canonicalize the payload with the JCS wrapper.
  Sign the domain prefix and canonical payload.
  Canonicalize the signed envelope.
  Calculate the event ID from the canonical envelope.
  Validate the envelope byte limit.
  Publish the canonical envelope.
  IF publication makes a published event,
    Increase the sent counter to its specified maximum.
  ELSE,
    Continue.
  RETURN the event ID.

function calculate HLC(last HLC, wall milliseconds) -> next HLC or error
  Validate wall milliseconds against the integer limit.
  IF the author chain is empty,
    RETURN wall milliseconds and logical value 0.
  ELSE,
    Continue.
  IF wall milliseconds are more than the last physical value,
    RETURN wall milliseconds and logical value 0.
  ELSE,
    Increase the last logical value by 1 in the integer limit.
    RETURN the last physical value and the increased logical value.

function publish(directory, published name, bytes, context) -> publication result or error
  Make one random temporary file with exclusive creation in the directory.
  Write all bytes to the temporary file.
  IF the write gives an error,
    Close the temporary file.
    Remove the temporary name when possible.
    RETURN storage_error.
  ELSE,
    Continue.
  Flush the temporary file with File.Sync.
  IF the flush gives an error,
    Close the temporary file.
    Remove the temporary name when possible.
    RETURN storage_error.
  ELSE,
    Continue.
  Close the temporary file.
  IF the close gives an error,
    Remove the temporary name when possible.
    RETURN storage_error.
  ELSE,
    Continue.
  IF context cancellation exists,
    Remove the temporary name.
    RETURN canceled.
  ELSE,
    Continue.
  Make the published hard link and keep the link result.
  IF the link result is a name collision,
    Validate the published regular-file type and bounded bytes.
    IF the existing bytes are different from the supplied bytes,
      Remove the temporary name.
      RETURN the caller error for different bytes.
    ELSE,
      Set the publication result to same.
  ELSE,
    IF the link result contains an error,
      Remove the temporary name when possible.
      RETURN storage_error.
    ELSE,
      Set the publication result to new.
  Remove the temporary name.
  IF removal fails after the hard link,
    Write one bounded recovery warning.
  ELSE,
    Continue.
  RETURN the publication result.

function scan author chain(project, after sequence) -> last event and selected event or error
  Read the author directory names.
  Ignore specified temporary names.
  Reject each other unexpected name or file type.
  Sort published names by author sequence.
  Set the expected sequence to 1.
  Set the predecessor to null.
  FOR EACH published name,
    Read at most 131073 event bytes.
    Validate the stored event.
    Validate the expected sequence, predecessor, and increasing HLC.
    IF this is the lowest sequence above after sequence,
      Keep this selected event.
    ELSE,
      Continue.
    Set the predecessor to this event ID.
    Set the last event to the verified event.
    Increase the expected sequence by 1 in the integer limit.
  RETURN the last event and selected event.

function validate stored event(project, published name, bytes) -> verified event or error
  Decode the envelope with the strict JSON rules.
  Compare stored bytes with canonical envelope bytes.
  Validate the project, epoch, author, recipient, key, and message schema.
  Compare the event ID with the published-name digest.
  Validate the Ed25519 signature and canonical base64url text.
  RETURN the verified event.

function history(project, after sequence) -> history result or error
  Scan the full author chain.
  IF a selected event exists,
    RETURN that event and its author sequence.
  ELSE,
    RETURN an empty event list and after sequence.

function status(process snapshot) -> status result or error
  Calculate uptime from monotonic elapsed time.
  Limit uptime to 9007199254740991.
  Validate the start time against the integer limit.
  Read the bounded process counters and last operational error.
  RETURN the process snapshot.

function write response(request ID, result, output, logs) -> output result or error
  IF the result is an error,
    Increase the rejected counter to its specified maximum.
    Classify the public code and fixed message.
  ELSE,
    Continue.
  Write one bounded sanitized outcome log.
  Encode one response of 131072 bytes or less.
  Write the response and LF to stdout.
  RETURN the output result.
```

## 3. Decisions and exact contracts

### 3.1 Minimal implementation shape

| Choice | Contract and reason |
| --- | --- |
| Go `1.26.8` through installed mise | The Forge row selects it. Build must add the repository pin before product code. |
| Module `github.com/qops1981/inter-harness-message-relay` | This matches the Git origin. |
| JCS `v1.0.1` | D25 approves it. Its observed API is `Transform([]byte) ([]byte, error)`. |
| Standard library otherwise | It supplies Ed25519, SHA-256, randomness, JSON, flags, files, contexts, time, errors, logs, and tests. |
| Distinct private identifier types | Project, participant, harness, session, and event identifiers cannot mix accidentally. |
| Wrapped classified errors | Front ends use `errors.Is` or `errors.As`, never message text. |
| One sequential core with two front ends | Stdio and CLI call the same concrete operations. No interface, worker pool, queue, or request goroutine. |
| Complete chain scan | Immutable history remains the only sequence, predecessor, HLC, and tamper authority. |
| Same-filesystem temporary file and hard link | `os.Root.Link` gives no-replace publication without an overwrite fallback. |
| Process-crash contract | File sync precedes the link. S01 does not promise power-loss or network-filesystem durability. |

Only JCS is a runtime dependency. The implementation uses one internal JCS
wrapper and local vectors. It does not import Testify, `x/sync`, `fsnotify`, or
Syncthing packages.

### 3.2 Literal limits

All maximum values are inclusive. Byte counts use UTF-8 bytes.

| Item | S01 value |
| --- | --- |
| Request record | 1–131072 bytes before LF, including an optional final CR. Read at most 131073 bytes. |
| CLI parameter JSON | 1–131072 bytes. LF is not required. |
| Protocol response | At most 131072 bytes before LF. |
| Message body | 1–16384 decoded UTF-8 bytes. No trim or normalization. |
| Stored envelope | 1–131072 canonical JSON bytes. No LF or trailing bytes. |
| `project.json` | At most 4096 bytes. |
| Private seed | Exactly 32 raw bytes. |
| JSON nesting | At most 8 open containers, including the root object. |
| Object members | At most 16 members in each object. |
| Version offer | 1–8 distinct positive integers. Version 1 must occur. |
| Protocol integer | `0` or `[1-9][0-9]*`, at most 9007199254740991. |
| Project epoch | Exactly 1. |
| Author sequence | 1–9007199254740991. Use 16 decimal digits in a final name. |
| HLC value | Each value is 0–9007199254740991. |
| History page | Zero or one event. `after_seq` defaults to 0. |
| Request ID | 1–64 ASCII characters in `[A-Za-z0-9_-]+`. |
| Project ID | `p-` and exactly 32 lowercase hexadecimal digits. |
| Participant ID | `u-` and exactly 32 lowercase hexadecimal digits. |
| Harness or session ID | `h-` or `s-` and exactly 32 lowercase hexadecimal digits. |
| Digest | `sha256:` and exactly 64 lowercase hexadecimal digits. |
| Public key or signature | Canonical unpadded base64url with 43 or 86 characters. |
| Status response | At most 4096 bytes. |
| Log record | At most 2048 bytes before LF. |
| Counter and uptime | Stop increasing at 9007199254740991. |
| Concurrency | One operation in progress. No internal request queue. |

The actual envelope and response encodings must pass their byte limits. S01
selects no artifact, batch, wait, claim, peer, presence, or queue limit.

### 3.3 Strict JSON and connection contract

Validation order is framing, JSON and resource rules, exact schema, connection
state, project trust, operation parameters, chain, integer allocation, and
publication. A rejected request performs no later step.

Strict JSON rejects these inputs at every object depth:

- Empty input, whitespace-only input, a non-object root, a BOM, invalid syntax,
  trailing data, or a second root value.
- Duplicate decoded names, including names that become equal after an escape.
- Invalid UTF-8, invalid surrogate pairs, or isolated surrogates.
- More than 8 open containers or more than 16 members in an object.
- Signed, fractional, exponential, negative-zero, or oversized integer tokens.
- Unknown, case-mismatched, missing, null, or wrongly typed fields.

The validator checks numeric tokens before JCS changes their representation.
The JCS wrapper owns canonical ordering, escaping, number formatting, and
rejection of duplicate decoded names. It rejects invalid UTF-8 and surrogate
pairs before it calls the dependency. It does not repair Unicode.

LF ends a record. The reader removes one CR only when CR immediately precedes
LF. Unicode line separators remain JSON string content. An oversized record or
partial record at EOF gets one error with a null request ID and exit 2. Clean
EOF gets no response and exit 0. Other complete invalid records get one error,
and stdio continues. An unsafe or unavailable request ID becomes null.

A connection starts uninitialized. Only `initialize` is valid then. Successful
initialization selects version 1 and these capabilities in this order:
`send`, `history`, `status`, `shutdown`. A second initialization fails.
Shutdown writes its response before exit. A response write failure exits 1.
A published event is never removed because its response was lost.

### 3.4 Public request, response, and CLI schemas

Every unmarked field is mandatory. No extension field is permitted.

| Object | Exact fields |
| --- | --- |
| Initialize request | `id`, `op:"initialize"`, `params`. It has no `version`. |
| Initialize params | `versions`, optional `create`. Absence means open. |
| Create | `participant_id`, `recipient_id`, `recipient_public_key`. |
| Later request | `version:1`, `id`, `op`, `params`. |
| Send params | `to`, `body`. |
| History params | Optional `after_seq`. |
| Status or shutdown params | No fields. |
| Success response | `version:1`, `id`, `ok:true`, `result`. No `error`. |
| Error response | `version:1`, `id` or null, `ok:false`, `error`. No `result`. |
| Error | `code`, fixed `message`. |
| Initialize result | `project_id`, `project_epoch`, `participant_id`, `public_key`, `signing_key_id`, `recipient_id`, `capabilities`. |
| Send result | `event_id`. |
| History result | `events`, `next_after_seq`. |
| History item | `event_id`, `envelope`. |
| Status result | The fields in section 3.9. |
| Shutdown result | No fields. |

History returns the lowest author sequence above `after_seq`. A result uses that
sequence as `next_after_seq`. An empty result keeps `after_seq`.

Startup scope uses these flags:

```text
[--root DIR] --project-id ID --harness-id ID --session-id ID
```

The default root is `.ihr` in the process working directory. The relay resolves
the root to an absolute path before filesystem access. An explicit root keeps
standalone and read-only-project use possible. Create can make the root with
mode 0700 when its parent exists. Open does not make a missing root. Scope IDs
are validated before filesystem access. Direct forms use the standard `flag`
package:

```text
relay [scope flags] stdio
relay [scope flags] initialize --id ID --params JSON
relay [scope flags] send       --id ID --params JSON
relay [scope flags] history    --id ID --params JSON
relay [scope flags] status     --id ID --params JSON
relay [scope flags] shutdown   --id ID --params JSON
```

`--params` uses the same parameter schema. Direct operations other than
initialization open project state and select version 1 internally. Direct
shutdown ends only its process. Bad CLI syntax returns a protocol error and a
sanitized JSON log. No private seed is accepted on the command line.

Exit 0 means success or clean stdio EOF. Exit 2 means framing, request, protocol,
trust, event-validation, integer, or cancellation failure. Exit 1 means storage,
output, or unexpected internal failure. A recoverable stdio error is not sticky.

### 3.5 Stable errors and precedence

| Code | Exact message | Condition |
| --- | --- | --- |
| `invalid_request` | `Invalid request.` | JSON, schema, scope ID, flag, or parameter failure. |
| `record_too_large` | `Record exceeds limit.` | Record or CLI parameter exceeds 131072 bytes. |
| `truncated_record` | `Record lacks LF.` | EOF follows a partial record. |
| `unsupported_version` | `Version 1 is required.` | The offer lacks 1, or a later version is not 1. |
| `not_initialized` | `Initialize first.` | A recognized operation occurs before initialization. |
| `already_initialized` | `Connection is initialized.` | Initialize occurs again. |
| `unsupported_operation` | `Operation is unavailable.` | A well-formed operation is outside S01. |
| `project_exists` | `Project already exists.` | Create finds any project directory. |
| `project_missing` | `Project does not exist.` | Open finds no project directory. |
| `invalid_project` | `Project state is invalid.` | Project state, trust, scope, file type, or seed is invalid. |
| `invalid_recipient` | `Recipient is not registered.` | `to` is not the configured recipient. |
| `body_limit` | `Body length is invalid.` | Body has 0 bytes or more than 16384 bytes. |
| `integer_limit` | `Event integer exceeds limit.` | Sequence, HLC, or wall time exceeds its limit. |
| `invalid_event` | `Stored event is invalid.` | Stored schema, bytes, name, digest, signature, trust, chain, or HLC is invalid. |
| `storage_error` | `Storage operation failed.` | A filesystem operation fails. |
| `canceled` | `Operation was canceled.` | Context cancellation occurs before acceptance. |
| `internal_error` | `Operation failed.` | An unexpected failure or oversized encoded response occurs. |

Schema validation precedes connection-state validation. An unknown but valid
operation name gets `unsupported_operation`. Operation names contain 1–32
lowercase ASCII letters or underscores. Project-state checks precede message
checks. Message checks precede cancellation, chain, and integer checks. The
publisher checks cancellation again immediately before the hard link.

Initialization error cases are exact:

| Case | Result |
| --- | --- |
| Create finds an existing project directory | `project_exists`, no write. |
| Open finds no project directory | `project_missing`, no write. |
| Marker or seed is missing, incomplete, oversized, unsafe, or inconsistent | `invalid_project`, no write. |
| A permission or filesystem read fails | `storage_error`, no write. |

Connection lifecycle cases are exact:

| Case | Result |
| --- | --- |
| Complete recoverable request error | One error response, then read the next record. |
| Oversized or truncated record | One error response, then exit 2. |
| Clean EOF | No more response, then exit 0. |
| Successful shutdown | One success response, then exit 0. |
| Output failure | Exit 1 without a rollback. |

### 3.6 Exact message event

The signed envelope contains only `payload` and `signature`. The payload has
these fields:

| Field | Exact value or rule |
| --- | --- |
| `version` | Integer 1. |
| `project_id` | Configured project ID. |
| `project_epoch` | Integer 1. |
| `type` | `message`. |
| `author` | Local participant ID. |
| `signing_algorithm` | `ed25519`. |
| `signing_key_id` | SHA-256 digest of the raw author public key. |
| `author_seq` | Prior verified sequence plus 1, or 1. |
| `prev` | Null at sequence 1, otherwise the prior event ID. |
| `created` | Only `physical_ms` and `logical`. |
| `to` | Registered recipient ID. |
| `body` | Validated unchanged body. |

`artifact`, `in_reply_to`, and event `request_id` are absent. Broadcast and work
status types are unavailable.

The signature input is ASCII `IHR-EVENT-V1`, one zero byte, and the RFC 8785
canonical payload. Ed25519 signs that input. The event ID is the SHA-256 digest
of the canonical signed envelope. Keys and signatures use canonical unpadded
base64url. Decode, length-check, and identical re-encoding are mandatory.

The first HLC uses wall milliseconds and logical value 0. A later event uses
new wall milliseconds and logical 0 when wall time increases. Otherwise, it
keeps the prior physical value and adds 1 to the logical value. Stored HLC pairs
increase lexicographically.

The JCS test table includes at least these cases:

| Input | Expected result |
| --- | --- |
| ` { "b":2,"a":1 } ` | `{"a":1,"b":2}` |
| `{"z":[3,{"b":2,"a":1}],"a":0}` | `{"a":0,"z":[3,{"a":1,"b":2}]}` |
| `{"s":"\u20ac\u000F\u000a\/"}` | `{"s":"€\u000f\n/"}` |
| `{"s":"\\ud800"}` | `{"s":"\\ud800"}` |
| RFC 8785 number-format vector | Exact RFC 8785 output. |
| Non-BMP name before `\ue000` | UTF-16 name order. |
| Duplicate decoded names | Error. |
| Invalid UTF-8, syntax, or surrogate pair | Error. |

Each successful output is idempotent. A fixed seed and literal canonical payload
also produce an independently calculated signature and envelope digest. A
product sign-and-verify round trip is not the only oracle.

### 3.7 Local state and trust

S01 uses only this layout. `ROOT` defaults to `PROJECT_WORKING_DIRECTORY/.ihr`:

```text
ROOT/projects/PROJECT_ID/
  project.json
  events/PARTICIPANT_ID/SEQUENCE-EVENT_DIGEST_HEX.json
  local/harnesses/HARNESS_ID/sessions/SESSION_ID/identity.seed
```

All durable runtime state and temporary files remain below `ROOT`. Runtime logs
remain on stderr and do not create hidden log files. Temporary names are
`.tmp-` and 32 lowercase hexadecimal digits. S01 creates no artifact, queue,
cursor, lease, peer, or archived path.

`project.json` is the canonical JSON initialization marker. It contains exactly
`version:1`, `project_id`, `project_epoch:1`, `administrator`, and
`participants`. The administrator is the local participant. `participants`
contains exactly the local participant first and the recipient second. Each
participant object contains exactly `participant_id`, `public_key`, and
`signing_key_id`. The IDs and public keys are distinct.

The operator supplies the recipient ID and public key. The relay generates the
local seed and derives its public key. The recipient private key never enters
the relay. The scoped seed path binds the local identity to the harness and
session. Project trust does not duplicate those local IDs. The operator owns an
offline recovery copy. S01 adds no backup or seed-output operation.

On POSIX systems, directories use mode 0700 and files use mode 0600. Git ignore
rules are not a security boundary. Windows uses the ACL of an operator-private
root. Managed files must be regular files.
Managed paths must not contain symlinks. `os.Root` constrains relative access.
The operator must prevent another same-account process from replacing managed
mounts or paths concurrently.

Create exclusively makes the project directory and required directories. It
publishes `project.json` last. A failed create can leave state without the
marker. A later create returns `project_exists`, and a later open returns
`invalid_project`. A failed create never regenerates a seed. Open never repairs,
deletes, or overwrites invalid state.

Open validates the bounded marker and seed, exact trust schema, scope, file
types, keys, and seed-derived key. History and send revalidate stored events on
every call. A final filename must match the author, sequence, and event digest.
Unknown directory entries are invalid. Exact temporary names are ignored.

### 3.8 Publication and tamper boundary

Publication creates a random exclusive temporary file beside the final file. It
writes all bytes, calls `File.Sync`, closes the file, and creates a hard link to
the final name. The hard link is the acceptance point.

| Publication case | Required result |
| --- | --- |
| Write, sync, close, cancellation, or link fails before acceptance | No new final file. Remove only the operation temporary name when possible. |
| Process stops before the link | A later scan finds no accepted event. |
| Process stops after the link | A later scan finds one complete accepted event. |
| Final file contains the same bytes | Return an identical result without replacement. |
| Final file differs or is not regular | Return the caller-classified conflict error without replacement. |
| Temporary cleanup fails after acceptance | Keep success and write one recovery warning. |
| A scan finds an exact temporary name | Ignore it. |

A file comparison reads at most the supplied byte count plus 1. Setup maps a
conflict to `invalid_project`. Event publication maps a conflict to
`invalid_event`. A filesystem without the required hard-link behavior returns
`storage_error`. There is no direct-write or rename fallback.

The guarantee covers process failure on validated local filesystems. It excludes
power loss, kernel failure, hardware-cache loss, NFS, SMB, and unvalidated
filesystems. Linux ext4, macOS APFS, and Windows NTFS need native publication
checks. Cross-compilation is not a filesystem check.

A history scan validates the full author chain before it returns one event. A
changed event, bad name, unsafe file type, duplicate sequence, gap, fork, bad
signature, wrong trust value, or non-increasing HLC returns `invalid_event`.
History returns no event content. Send publishes nothing. Files remain for
diagnosis. S01 does not claim detection of deletion from the unreferenced tail.

### 3.9 Status and logs

Status contains exactly `started_at_ms`, `uptime_ms`, `sent`, `rejected`, and
`last_error`. Uptime uses monotonic elapsed time. Counters start at 0, reset on
restart, and stop increasing at 9007199254740991. `sent` increases after a new
event publication. `rejected` increases for each terminal request error.

`last_error` is null initially. It later contains exactly `code` and `message`
for the most recent `invalid_project`, `invalid_event`, `storage_error`,
`integer_limit`, or `internal_error`. Other errors do not replace it.

Logs use `log/slog` with a JSON handler on stderr. Each record contains `time`,
`level`, and `msg`. It can also contain validated `project_id`,
`participant_id`, `harness_id`, `session_id`, `request_id`, `event_id`, and
`code`. No other field is permitted. Levels are lowercase.

Messages are `startup`, `initialize`, `send`, `history`, `status`, `shutdown`,
`request_rejected`, `recovery_warning`, or `internal_error`. Messages have at
most 64 ASCII bytes. Each request produces at most one outcome log and one
recovery warning. Startup and shutdown each produce one log.

Logs never contain bodies, raw JSON, envelopes, keys, signatures, seeds,
credentials, paths, file content, or raw error text. An oversized log becomes a
fixed minimal `internal_error` log. Stderr failure is best-effort. Stdout never
contains a log or plain text.

## 4. Minimal surface

### PPP writes now

- `sprints/S01-signed-local-message/SPEC-S01-signed-local-message.md`
- `SPRINTS.md`, with the human-requested prior-sprint-gap reflection question.
- `.gitignore`, with the project-local `.ihr/` runtime root.
- `.forge/run.jsonl`, with append-only PPP and gate entries.

PPP changes no product file and no Forge-owned state field.

### After human approval only

| Path | Responsibility |
| --- | --- |
| `.mise.toml` | Exact Go pin. |
| `go.mod`, `go.sum` | Module and exact JCS dependency. |
| `cmd/relay/main.go` | Call the core and exit. |
| `internal/relay/relay.go` | Process, front ends, dispatch, status, and logs. |
| `internal/relay/event.go` | Strict JSON, JCS wrapper, event, signature, and HLC. |
| `internal/relay/store.go` | Scoped state, publication, validation, and history. |
| `internal/relay/relay_test.go` | B1–B25 tests, vectors, fuzz checks, example, and subprocess proof. |
| `DEVELOPER_GUIDE.md` | S01 schemas, examples, limits, errors, and storage caveats. |

### At S01 clearance only

| Path | Responsibility |
| --- | --- |
| `sprints/S01-signed-local-message/SPRINT.md` | Record the required sprint clearance reflections. |

`Run` is the only initial exported core identifier. No `pkg` tree, repository
layer, factory, custom interface, fixture framework, or generic validator is
approved.

## 5. Exclusions and ceilings

S01 excludes peer networking, TLS, reconciliation, artifacts, delivery queues,
claims, acknowledgment, waits, presence, control events, installers, and harness
bridges. It excludes broadcasts, work status, replies, request links, key
rotation, close, reopen, and purge.

S01 adds no database, mutable tip, cache, index, lock protocol, compression,
notifications, telemetry server, tracing framework, CGO, `unsafe`, generics, or
build tags. One process owns the local author. The operator stops stdio before a
direct command uses that author.

`ponytail:` a complete chain scan costs O(n) file reads and O(n) names. Add a
rebuildable index only after measured scan cost requires it.

## 6. Fidelity and minimalism gate

### 6.1 Normative-source verification

The final pass read `AGENTS.md`, `SPEC.md`, `DEVELOPER_GUIDE.md`, `CONTEXT.md`,
`DESIGN.md` sections 2–3 and 9, `DECISIONS.md` D15–D27, `SPRINTS.md`, the
S01 `SPRINT.md`, and the official RFC 8785 text. The following table records
the source disposition.

| Source contract | S01 disposition |
| --- | --- |
| First uncleared sprint only | Retained. No S02 or S03 behavior or surface remains. |
| Create one project, one author, and one recipient | Retained in initialization, trust, and state schemas. |
| Strict bounded JSONL plus direct CLI | Retained with literal framing, schema, response, root, and exit contracts. |
| RFC 8785, Ed25519, SHA-256, immutable event | Retained with exact domain prefix and envelope. Official JCS input, serialization, UTF-16 sorting, and UTF-8 rules agree. |
| Verify stored events before history | Retained for every history and send scan. |
| Restart and byte-change proof | Retained as B25. |
| Protocol-only stdout and JSON-log-only stderr | Retained as B23. |
| Status and shutdown | Retained with the smallest fixed S01 result schemas. |
| Go, standard library, one JCS wrapper | Retained. JCS is the only runtime dependency. |
| Platform publication contract | Narrowed to process failure on validated local filesystems. |
| Public guide update and runnable example | Retained in the approved surface. |
| Queues, delivery, peers, presence, control, artifacts | Removed from behavior and implementation scope. |

The limits not fixed by the source documents are S01 interface choices. They
are literal here because `SPEC.md` and the sprint delegate them to PPP. No
source supplies a conflicting value.

### 6.2 Reduction from the prior draft

The prior draft had 45 behaviors and 1128 lines. This pass has 25 behaviors.
It combines cases only when one named contract and one table-driven test remain:
initialization errors, framing errors, connection lifecycle, publication, and
invalid project history.

Removed independent behaviors covered internal steps, duplicated invariants, or
future scope. Removed items include separate counter, temporary-file, collision,
empty-history, stdout-only, and individual state-error behaviors. Their required
cases remain in the owning contract tables. S02/S03 admission, delivery, queue,
and peer contracts remain excluded.

### 6.3 Evidence-based PPP questions

| Question | Final evidence and disposition |
| --- | --- |
| Minimalism | `git ls-files` and repository search show no product code to reuse. Go covers all functions except approved JCS. No second dependency or state authority remains. |
| Altitude | Framing, initialization, signing, publication, chain validation, response writing, status, and logging are independently consequential. Token checks remain inside decoding. |
| Generality | Publication accepts bytes and reports new, identical, or conflicting state. Callers classify conflicts. The JCS wrapper accepts RFC numbers while protocol decoding rejects them. |
| Root cause | Both front ends enter the same strict schemas and core operations. Send and history rescan authoritative bytes instead of trusting cached state. |
| Form | Limits, schemas, errors, publication cases, and log fields are finite tables. Fixed S01 policy is not configuration or a generic rules engine. |
| Tech fit | Go `1.26.8`, `os.Root.Link`, standard APIs, and JCS `Transform` were read from the selected local toolchain and cached module. |

No question fires after this pass. No unchanged-artifact gate question repeated.
The design has no unresolved question.

### 6.4 ASD-STE100 Issue 9 CHECK

Source: **ASD-STE100 Issue 9 (2025-01-15), per PROVENANCE.md**. The installed
source was last reviewed on 2026-09-02. The complete installed rules and the
`ste-lookup` dictionary were used.

CHECK covers every B1–B25 statement and every nonempty pseudocode line. Behavior
requirements are descriptive. Pseudocode actions are procedural. Every checked
line uses at most 20 raw whitespace tokens, has no semicolon, and keeps one
term for each concept.

Applied dispositions:

| Finding | Disposition |
| --- | --- |
| Unapproved ordinary word | Replaced with the approved alternative shown by `ste-lookup`. |
| Word with multiple entries | Checked the part of speech and retained only its approved use. |
| Computing or cryptographic term absent from the dictionary | Retained as a technical noun or technical verb under Rules 1.5, 1.8, and 1.12. |
| Control word such as `IF`, `ELSE`, `WHILE`, `RETURN` | Retained as PPP notation with one stable meaning. |
| Function contract | Retained as PPP notation, not an implementation-language declaration. |
| Long or compound instruction | Split until one action and no more than 20 words remained. |
| Condition after an instruction | Moved before the instruction under Rule 5.4. |
| Passive or progressive verb | Rewritten in active simple present or imperative form. |

Technical nouns include relay, JSON, schema, Unicode, JCS, CLI, stdio, stdout,
stderr, request, request ID, result, process, connection state, Ed25519,
SHA-256, envelope, event, event ID, HLC, project trust, author chain,
filesystem, hard link, and process snapshot. Technical verbs include decode,
canonicalize, encode, validate, publish, sign, scan, and sort. Each term keeps
one meaning. No technical noun is also used as a verb.

Mixed-entry checks retain `as` only as an approved preposition, `close`,
`complete`, and `increase` only as approved verbs, and `code`, `last`, `next`,
and `time` in their approved parts of speech. `get` means obtain. `direct CLI`, `operational
error`, `create`, and `run` are computing terms or labels. `RETURN`, `IF`,
`ELSE`, and `WHILE` are PPP control notation.

Final disposition: no unresolved STE finding. The reproducible lexical result,
lookup exit counts, checked-line count, and checked-passage hash are recorded in
the PPP log entry.

## 7. Verification and handoff

The design checks must confirm:

- State-derived header values and the current design assignment.
- Consecutive B1–B25 identifiers and one-line behavior statements.
- At most 20 words and no semicolon in every behavior and pseudocode line.
- One matching `ELSE` for every pseudocode `IF`.
- Dictionary lookup coverage for every lexical token in checked passages.
- No product changes and no Forge-owned state changes.
- No normative-value conflict in the source list from section 6.1.

After approval, TDD implements one B-ID at a time. The B25 integration proof:

1. Starts a real relay process with isolated local storage.
2. Creates the project and participant identities through public operations.
3. Sends one direct message through JSONL.
4. Stops and restarts the process.
5. Reads the same event ID and canonical signed envelope from history.
6. Verifies the signature independently with the initialization public key.
7. Changes the stored event bytes.
8. Requires `invalid_event` without event content.
9. Parses stdout and stderr separately against their schemas.

Future acceptance commands include `go test ./...`, `go vet ./...`, `go test
-race ./...`, JCS vector tests, fuzz checks for strict JSON and stored events,
`go mod verify`, `govulncheck`, runnable examples, formatting, and native
publication checks on supported local filesystems. PPP does not claim those
unwritten checks passed.

At S01 clearance, answer all eight reflection questions in `SPRINTS.md`.
The prior-sprint-gap answer for S01 is `none` because no sprint precedes S01.
Later sprints must identify omissions in each cleared sprint that affected them.

The default `.ihr/` root keeps all durable runtime state in one ignored project
directory. `--root` remains an override. Git ignore rules do not replace file
permissions or input validation.

Unresolved design questions: **none**. Pending items are human approval,
implementation, product checks, native platform evidence, vulnerability
checking, and independent Forge verification. PPP stops here.

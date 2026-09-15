# Inter-Harness Message Relay development policy

## Mission

Build one small tool that exchanges authenticated, project-scoped messages and
artifacts between harness sessions, reconciles after disconnection, and exposes
bounded presence. Use the canonical terms in `CONTEXT.md`. Read `DESIGN.md`
§§2–3 and 9 and `DECISIONS.md` D15–D27 before changing scope or architecture.
Before implementation planning or work, read `SPRINTS.md` and select only its
first uncleared sprint.

## Simplicity gate

Before adding code, configuration, a dependency, or a module:

1. Show that reliable send, receive, replication, authentication, retention, or
   liveness needs it now.
2. Reuse existing project code, then Go's standard library, then native
   platform behavior.
3. Choose the smallest direct implementation. One implementation stays
   concrete; extract a shared interface only when a second real adapter needs
   it.
4. Prefer complete copies and cheap reads over coordination or reconstruction.
5. Keep process lifecycle, scheduling, orchestration, workflows, dashboards,
   plugin hosting, general database/message-bus behavior, and harness-specific
   integrations outside the core.

A feature that does not pass this gate is complete when it is declined and the
reason is recorded.

## Required workflow

- Apply the installed `ponytail` skill at full intensity to every design,
  implementation, refactor, fix, and review.
- Run every nontrivial implementation through `forge`; do not bypass it with
  direct coding.
- Use `ppp` before code. Write numbered EARS-lite behaviors, plain-English
  pseudocode, decisions, and the expected file surface in one feature spec.
  Run the minimalism gate and stop for human approval.
- Use `tdd` after approval. Implement one behavior at a time with strict
  RED → GREEN → REFACTOR, frozen RED tests, the full suite after each GREEN,
  and final spec-fidelity verification.
- For a trivial change, run the PPP gate inline. If observable behavior changes,
  still write the failing check before the fix. Pure documentation changes do
  not need artificial tests.
- Forge starts only after its Git repository, worktree, panel, asset, and
  enforcement-hook preflight succeeds. Report a failed preflight. Use a
  documented manual-gate variant only with explicit human approval; never label
  it hook-enforced.
- Keep the main checkout read-only and implement in a dedicated worktree once
  this directory is a Git repository.
- Get separate human approval before commit, push, PR creation, deployment, or
  merge.
- After the dogfood activation check in `SPEC.md` §14 passes, use the relay for
  real project coordination in shadow mode. Keep an independent fallback until
  the human approves relay-only use. Convert each dogfood defect into a failing
  regression check before fixing it.

## Implementation constraints

- Core language: Go; standard library first; pure-Go build where practical.
- Install every language toolchain through a version manager and pin its version
  in the repository. Reuse an installed manager when possible; obtain human
  approval before installing one. Do not replace the system language runtime.
- Use bounded goroutines, `context.Context`, typed protocol structs, monotonic
  durations, atomic file publication, wrapped errors, and strict bounded input
  decoding. Run applicable fuzz and race checks.
- Reach for `unsafe`, CGO, plugins, reflection frameworks, generics, or build
  tags only after a concrete approved need.
- Approved dependency: `github.com/gowebpki/jcs`, isolated behind one internal
  RFC 8785 wrapper with local vectors. Use `x/sync/errgroup` only for a proven
  multi-goroutine error-propagation need. Defer `fsnotify`; never import
  Syncthing internals. Run `govulncheck` in dependency/release verification.
- Local harness seam: versioned stdio/JSON plus a direct CLI.
- Harness-specific bridges are separate projects and contain translation only;
  the core never imports Pi, Claude, or other harness SDKs.
- Optional Linux/macOS helpers use POSIX `sh`; the core never requires a shell
  or Ruby runtime.
- Every public Go package and exported identifier has an idiomatic Go doc
  comment. Public integration flows have runnable `_test.go` `Example`
  functions; verify them with `go test ./...`.
- Comment internal code only when its reason is not clear from the code.
- Runtime visibility uses JSON Lines on `stderr`, bounded metrics through the
  existing `status` operation, and existing IDs for correlation. Add no
  telemetry server or tracing framework without measured diagnostic need.
- New nontrivial behavior leaves the smallest runnable regression check.
- A public interface or integration behavior change updates
  `DEVELOPER_GUIDE.md` in the same reviewed change.
- Finish with the fewest files, dependencies, exported names, configuration
  fields, and lines that satisfy the approved behavior.

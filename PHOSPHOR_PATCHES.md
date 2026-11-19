# Phosphor patch-for-the-patch: telemetry command completion

This is a downstream follow-up to the telemetry lifecycle patch in Dagger
1.0.0-beta.23, base f0a3d728cf7012e0954f772b5dc5c8471648afaa, and its
otel-go base 132b1dd82eb2. It is deliberately labelled in each changed code
path so an upstream merge conflict does not accidentally restore blocking
remote export. It does not replace upstream history.

Preserve these behaviors on rebase:

- Drain real local command output and preserve command exit status.
- Give remote provider shutdown one 250 ms caller-controlled budget, with
  concurrent signals and a deadline that survives an uncooperative SDK.
- Never synchronously re-export remote metrics from engine stream consumers.
  Queue only remote destinations; keep local frontend metrics independent.
- Freeze aggregation, bucket and exemplar slices; retain context values.
- Bound waiting batches and encoded payload including in-flight data; make
  overflow, cancellation and unfinished flush visible.
- Keep the upstream engine protocol/image version; the release suffix identifies
  this client fork, not a new engine protocol.

Acceptance: telemetry package race tests, native Linux CLI configuration/output
tests, and the real CLI against healthy, 503, stalled and refused sinks. Check
exact stdout, real command failure and live shared-backend trace/log readback.
A smaller shutdown deadline alone is insufficient: the synchronous metric
forwarding path independently blocked larger-output commands in the reproducer.

Silent check execution and silent `api with-session` sessions also omit local
retaining progress exporters. Check status still comes from evaluated artifacts;
the session's child still owns stdio. Independently configured traces, logs and
metrics remain enabled. Check listing keeps its primary stdout frontend even
when progress is silent. `TestHeadlessCommandTelemetry` covers these command
boundaries, including visible mode and independent exporter admission.

Native qualification must preserve a real passing check, failing check, artifact
export, scalar status and outer failure exit in one session, with matching CLI
and engine telemetry. Use module-qualified check links from the real workspace
catalog. A release probe does not substitute for the consuming repository's
complete diagnostic report.

Report-mode final rendering, DOT output and exit normalization share the existing
frontend mutex with telemetry ingestion. Public final renders use the same owner
without relying on telemetry shutdown to imply callback quiescence. The finite
public reporter/exporter race regressions preserve subsequent failure rendering
and cover both direct final rendering and Run finalization.

Upstream removal criterion: equivalent tests pass against the proposed upstream
replacement without this follow-up. An HTTP 200 alone is not delivery proof.

Native placement startup carries the same client metadata as the subsequent
session, including its session identity. BuildKit Wait/Info must not discover a
different worker through an anonymous preflight before the native session is
placed. Preserve unrelated outgoing gRPC metadata and metadata-free callers.

Git labels also carry `dagger.io/git.worktree`, the digest of the resolved local
worktree root, independent of commit and working subdirectory. Linked worktrees
remain distinct; symlink aliases resolve to the same identity.

The native client keeps its last validated Info worker in a small private receipt
under XDG state, scoped to that checkout and engine endpoint. Reads are confined
and validated; atomic replacement prevents partial concurrent writes. State
failures report an explicit status and leave the command's verdict unchanged.
Relative state roots are refused rather than writing into the checkout. Metadata
labels are copied before adding `dagger.io/engine.last` and its status.

The gateway remains the sole chooser: this is a preference, not authentication,
a worker selector or proof that a cached result exists. Older gateways ignore
it. Remembering and honoring a prior assignment, including exact fallback
reasons, requires the companion reviewed gateway change. Session identity and
ordinary admission continue to belong to that native placement service.

The startup wire regression and checkout-label tests cover these contracts. No
engine image or gateway deployment is implied by the source tests.

Hosted launchers may initialize this receipt from a verified earlier report.
The receipt contract is `$XDG_STATE_HOME/dagger/placements/<key>` (the normal
XDG state default applies), mode0600 in a private directory. Its JSON is
`{"version":1,"worker":"<serving engine>"}`. The key is hexadecimal SHA-256 of
`endpoint + "\u0000" + checkout`, where checkout is hexadecimal SHA-256 of the
resolved worktree-root path. Use the exact endpoint string passed to the client.
A later validated Info assignment replaces that initial preference.

The launcher owns reference provenance, not the client: prefer a completed main
report for the exact merge-base; otherwise use the nearest completed main
ancestor and state that fallback. Require a unique actual serving worker for
the checked tree and validate the report's repository/commit before seeding.
Keep the reference run, commit and selection reason in the check's report.
Missing or ambiguous evidence leaves the receipt absent; the gateway's normal
choice still yields a verdict. Do not write another checkout's state, redirect
straight to a worker, or claim the reference proves a cache hit. Older client
versions ignore this receipt; source compatibility and release qualification
remain separate from these local protocol tests.

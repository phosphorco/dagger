# Phosphor's Dagger patch catalogue

Phosphor maintains these fixes locally. Do not open upstream issues, pull requests,
or other reports for them. Every delta records its reason, failing regression
and drop condition here and in its pull request.

This release branch is based on the deployed upstream beta.23 commit
`f0a3d728cf7012e0954f772b5dc5c8471648afaa`. It separates the urgent cache repair
from the later upstream migration. All patches are source in this fork; the
rosetta-machine builder consumes a single immutable fork commit, never a patch file.

## Module object content and retained-cache boundary — PR 7

- **Upstream delta:** object content includes its actual type and defining
  module. Module function calls also include the stable object-content format
  version, so legacy equivalent objects cannot bypass the repaired collector.
- **Why patch now:** older checkouts otherwise receive another object's value
  from a retained local or shared cache. The error can be loud or silent;
  clearing a worker does not prevent the equivalence from forming again.
- **Regression:** `core/module_object_content_scope_test.go`,
  `TestModuleObjectContentDomain` and
  `TestModuleObjectContentUpgradeDoesNotServeLegacyEquivalence`. They distinguish
  same/different type and module, and warm legacy equivalents before expiring
  one producer and calling again with and without a Workspace input.
- **Drop when:** the selected upstream release scopes object content by type
  and defining module **and** prevents legacy calls in retained/imported caches
  from bypassing that scope, with these regressions still passing.
- **Merged source:** `300dcea7c4351313a892c45f7a8cf040d6223e5d`.

## Donor capture invalidation — PR 6

- **Upstream delta:** a completed donor capture whose representation changed
  requests part-source reselection, instead of being treated as an absent donor.
- **Why patch now:** the scan can otherwise report an unavailable imported
  filesystem part although another equivalent donor retains the snapshot.
  This reproduces one cause; it does not establish b1's physical loss cause.
- **Regression:** `dagql/cache_part_source_test.go`,
  `TestPartSourceScanReselectsChangedDonor`, through
  `AcquireEquivalentPartSource`. Busy/missing sources and encoder failures keep
  their distinct behavior.
- **Drop when:** the selected upstream scan reselects after invalidating a
  completed donor capture and this public source-selection regression passes.
- **Merged source:** `bd6a8e2601968e8e80bf38e0f4f2e38a412530b4`.

## Bounded Dang nested requests

- **Upstream delta:** the Dang SDK shares one request permit among nested
  goroutines and releases it when a request finishes.
- **Why patch now:** preserve the already deployed request bound and avoid
  uncapped fan-out when independent requests run together.
- **Regression:** `core/sdk/dang/shared/concurrency_test.go`,
  `TestNestedClientBoundsActiveRequestsAndCompletesQueuedWork`.
- **Drop when:** the selected upstream SDK bounds active nested requests across
  goroutines and completes queued work under this regression.

## Linear causal telemetry fan-out

- **Upstream delta:** construct causal link targets with a first-seen set,
  preserving order while avoiding repeated linear membership scans.
- **Why patch now:** large real check traces must not spend quadratic work
  retaining causality; this preserves the deployed telemetry behavior.
- **Regression:** `engine/clientdb/telemetry_causal_targets_test.go`,
  `TestCausalLinkTargetsPreservesFirstSeenCauses` and
  `TestCausalLinkTargetsRetainsLargeFanout`; `BenchmarkCausalLinkTargets` measures
  the large case without changing its meaning.
- **Drop when:** the selected upstream target collector preserves first-seen
  identity/order with equivalent linear work and these tests pass.
## Declared no-child execution admission

- **Upstream delta:** one engine-owned semaphore bounds declared no-child
  executions across sessions, holds capacity through cleanup, and excludes
  services and commands retaining Dagger API access. Negative CLI limits fail.
  The beta.23 base has no competing permit path; this is the deployed bound.
- **Why patch now:** preserve the deployed engines' resource contract during
  this release. Counting parents while descendants wait for their capacity can
  deadlock; releasing capacity before failure cleanup can exceed the bound.
- **Regression:** `engine/engineutil/exec_admission_test.go` covers cross-session
  admission, cancellation, cleanup, nested parents, declared leaves and the CA
  helper; `engine/server/execution_parallelism_test.go`,
  `cmd/engine/TestParallelismFlag`, and
  `core/schema/TestExecutionAdmissionNoChildLeafArguments` cover the boundaries.
- **Drop when:** the selected upstream constructor provides the same declared
  execution domain, lifecycle-bound permit and configuration validation, with
  all these tests passing unchanged.

## Engine-owned current module context

- **Upstream delta:** `Query.currentModule` is scoped to the engine cache. Its
  implementation-scoped module argument retains the native result number;
  resolution uses the existing operational schema-module acquisition, which
  prefers that healthy recorded result over an equivalent imported value.
- **Why patch now:** normal argument normalization and ID loading can substitute
  an imported equivalent for a freshly acquired native module. Imported local
  paths correctly refuse access, so a check moved between engines can lose its
  verdict. An imported `CurrentModule` value can produce the same refusal.
  The beta.23 deployed baseline reproduces it too; phosphor.10 did not introduce
  the guard. Its rate in ordinary baseline runs is not established.
- **Regression:** `core/schema/imported_current_module_context_test.go`,
  `TestImportedModuleDefinitionKeepsCurrentLocalContext`, uses two real caches
  and exported/imported module definitions. It covers definition-only reuse,
  current-module values from repaired and legacy producers, and a client ID
  retained across engines. Type discovery stays reused without an SDK call;
  native context remains usable, repeat reflection reuses locally, and direct
  foreign paths still refuse access. The unchanged beta.23 baseline fails.
- **Cost:** current-module reflection from another engine is recomputed locally.
  Portable definitions remain shared. No cache clear or foreign-path guard
  relaxation is needed; installed-module and session-resource checks remain.
- **Drop when:** the selected upstream version separates live module context
  from portable definition equivalence at argument acquisition, handle loading
  and current-module reuse, including legacy imported caches, with this
  regression and the foreign-context reader tests passing.

## Private per-execution memory ceiling

- **Upstream delta:** optional `execution.memoryMaxBytes` confines each primary
  OCI command and its descendants to a private cgroup v2 memory ceiling, with
  swap disabled and group OOM termination. The default remains disabled.
  Stricter existing limits are retained; byte ceilings round down to kernel
  pages. The runtime retains the cgroup until its local OOM evidence is read,
  before the existing cleanup removes it.
- **Why patch now:** a compiler started by an older client used about 60 GiB of
  a shared 64 GiB engine. Protection in a new repository checkout cannot cover
  that client or the next misbehaving command. The execution owner can.
- **Regression:** `engine/engineutil/execution_memory_test.go` covers the real
  OCI specification, stricter limits and page granularity, local OOM versus
  reclaim or ancestor kills, missing evidence, and tolerated native exits.
  `core/exec_memory_error_test.go` prevents a resource failure from acquiring a
  program-exit error with a zero exit code. Config/server tests cover optional
  numeric configuration and refusal of negative values before startup.
  Offshared kernel/API controls use existing client pins 7 and 8: an actual
  128 MiB allocation passes with the default and exceeds a 64 MiB limit by
  name with `ANY`, while a live sibling retains its instance, the daemon keeps
  its PID/start time and ordinary exit 7 remains exit 7.
- **Failure contract:** only a local cgroup OOM proves
  `ExecutionMemoryBoundExceeded`; reclaim and an ancestor's kill do not.
  Missing required kernel evidence is `ExecutionMemoryBoundObservationUnavailable`.
  A proved resource failure cannot be accepted by an arbitrary-exit caller.
  Docker's `OOMKilled` flag can become true for a confined descendant while
  the daemon remains running; use the named execution evidence, actual process
  identity and scoped kernel counters to distinguish that from daemon harm.
- **Cost and limits:** Linux cgroup v2 filesystem paths are required when
  enabled. This is a ceiling, not a memory reservation or aggregate admission
  policy. No successful cache entry is invalidated. A proposed 24 GiB pool
  ceiling leaves 11.38 GiB above the measured 12.62 GiB ordinary peak; two
  observed peaks leave 38.76 GiB of the 64 GiB parent before other work. Two
  ceilings leave 16 GiB for the daemon and helpers; the Go heap target does not
  bound daemon RSS. Pool activation is separate from this source change.
- **Drop when:** the selected upstream provides a private per-execution ceiling
  with local OOM proof before arbitrary exits are admitted, preserves cleanup
  and sibling isolation, and passes these regressions and old-client controls.

## Unrecoverable imported operation-none part — phosphor.13

- **Upstream delta:** when an imported top-level field result has no ready
  equivalent, usable offer, parent delegation or saved lazy operation, expire
  and retire it from lookup under the graph lock, then reselect and replay its
  recorded call after the acquire task settles. The native result supplies the
  original holder's part. The retirement error remains both reselect-class and
  `ErrUnavailablePart`; joined cleanup failures are never swallowed by retry.
- **Why patch now:** an imported operation-none Directory can otherwise remain
  a cache hit after its last offer expires, failing every demand. PR 6 handles
  a donor whose capture changed, not this no-source branch.
- **Safety/refusal:** revalidate the receiver and all scanned candidate facts
  before retirement. Replay uses `LoadReplayable`, which enforces the existing
  field replay restrictions. Inline outputs, non-field/nth recipes, missing
  servers refuse by name with the scanned candidates' reasons. A structurally
  non-replayable field is never run automatically: its dead root is retired and
  the demand refuses by name, so a later normally issued call can compute fresh.
  Replay failure retains candidate reasons and its own cause.
  Usable offers and native lazy operations retain their existing paths.
- **Regression:** `TestImportedOperationNoneDirectoryRecomputesExpiredOffer`
  exports a real operation-none Directory with an expired-only offer. Before
  the patch both demands fail, the next lookup serves the same imported row,
  and the native resolver runs zero times. Afterward the demand succeeds and
  the next lookup uses its natively computed replacement. Controls cover eight
  concurrent demands with one recomputation, a usable download without replay,
  missing replay context and a non-replayable field that must not execute during
  recovery or explicit replay, but whose next normal call must miss the dead row.
  `TestImportedPartRetirementPreservesRecoveryAndCleanupClasses` protects error
  identities and propagation of an independent cleanup failure.
- **Drop when:** the selected upstream no-source path retires unrecoverable
  imports across every lookup route and safely recomputes replayable calls,
  while preserving named refusals, holders and these regressions.

## Session-scoped declared Git clients — phosphor.14

- **Upstream delta:** successful declared-Git-client digests are shared within
  the owning engine session for one module source, caller host and workspace
  read epoch. The session clears the memo after its clients drain. Failed reads
  remain uncached and produce a fresh unreadable input on every call; non-empty
  declarations retain the existing per-session input.
- **Upstream origin:** change 14493, included in base `f0a3d728`, introduced
  the declared-client walk before each module-function selection.
- **Why patch now:** the client declaration walk precedes module-function
  selection. A measured package check made 1,439 stats and 481 reads of
  `dagger.toml`; runner round trips held a median check for about 285 seconds.
- **Regression:** `core/integration/module_git_client_reads_test.go` counts
  actual CLI declaration reads for one and twenty module selections. Focused
  core tests cover shared successful reads, failed-read recovery, source/host/
  epoch/session separation, cancellation and fresh unreadable digests; the
  engine session teardown test checks disposal.
- **Qualification:** the client-read regression is red on the serving
  phosphor.11 engine, whose exercised declaration reader and digest method are
  byte-identical to this phosphor.13 base. Exact-base and candidate native
  client-read proofs pass, with seven declared-Git trust controls. The focused
  function-boundary law compares memoised and unmemoised empty and non-empty
  values, and requires fresh unreadable values without failures. First-install
  qualification includes unchanged ordinary checks in two sessions while
  withdrawn; timing is observed from ordinary CI after return.
- **Residual:** editing a declaration during a session without changing its
  workspace read epoch is not noticed until the next session.
- **Drop when:** the selected upstream release shares successful declaration
  reads at the same session/source boundary while preserving failed-read
  no-reuse, non-empty session binding and explicit workspace-epoch changes,
  with the client-read and lifetime regressions passing.

# Phosphor's Dagger patch catalogue

Phosphor owns these changes and their releases. We maintain them locally; do not
open upstream issues, pull requests, or other reports for them. Each future fork
pull request updates this file with its upstream delta, reason, regression, and
drop condition. Keep independent changes separate so a future upstream rebase
can remove a superseded patch without reconstructing its purpose.

The current series descends from the reviewed fork base
`3991a28b9dbcfb6e9badbf35fa3b54e9419d07f2`. The two cache fixes below are Cole's
exact squash merges. This release also imports the runtime controls previously
kept in rosetta-machine's native-engine.patch; that file is a historical source
of the port, not an input to future builds. Release provenance names a single
fork commit. Selecting one newer upstream release and rebasing the complete
series remains a separate migration after this urgent fleet repair.

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

## Declared no-child execution admission

- **Upstream delta:** one engine-owned semaphore bounds declared no-child
  executions across sessions, holds capacity through cleanup, and excludes
  services and commands retaining Dagger API access. Negative CLI limits fail.
  The superseded per-exec slot/acquire/API-release path is removed, so the
  port does not take two permits or change which work the deployed bound covers.
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

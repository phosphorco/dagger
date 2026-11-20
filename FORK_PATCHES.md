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


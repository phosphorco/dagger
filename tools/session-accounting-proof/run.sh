#!/bin/sh
# Bounded unpublished source qualification under the existing owning grant.
set -eu
proof=tools/session-accounting-proof
# All imports/caches are private copies. Settings are soft, not CPU/RSS quotas.
test "$(go env GOVERSION)" = go1.27.0
go version > /out/toolchain.txt
sha256sum go.mod go.sum sdk/go/go.mod sdk/go/go.sum engine/distconsts/go.mod internal/version/VERSION "$proof"/dagger-module.toml "$proof"/main.dang "$proof"/run.sh "$proof"/server-tests.pattern "$proof"/executor-tests.pattern "$proof"/core-tests.pattern "$proof"/vet-baseline.json "$proof"/test-list.json "$proof"/cost/main.go "$proof"/verify-vet/main.go > /out/baseline-tool-source.sha256
# Checkpoints observe only this container's source/output/tmp/module paths. They
# are NOT a native API quota or a simultaneous physical-host allocation peak.
# A transient peak between checkpoints is unknown; no background sampler exists.
checkpoint() {
  phase=$1
  du -s -B1 /src /out /tmp /go/mod > "/out/scratch-$phase.txt"
  df -B1 --output=avail /src /out /tmp /go/mod > "/out/available-$phase.txt"
  used=$(awk '{n+=$1} END{printf "%.0f",n}' "/out/scratch-$phase.txt")
  # 20GiB is an observed checkpoint stop budget, never a hard quota claim.
  test "$used" -le 21474836480
  remaining=$((21474836480-used))
  awk -v required="$remaining" 'NR>1 && $1<required {bad=1} END{exit bad}' "/out/available-$phase.txt"
}
checkpoint pre
# Wrapper bootstrap cost is wall-timed separately; whole-leaf native cost also
# includes it. No host compiler, derived shared build cache or dependency fetch.
date -u +%FT%TZ > /out/wrapper-build-start.txt
go build -mod=readonly -trimpath -buildvcs=false -o /out/cost "$proof/cost/main.go"
date -u +%FT%TZ > /out/wrapper-build-end.txt
/out/cost /out/compile-vet-verifier-cost.json go build -mod=readonly -trimpath -buildvcs=false -o /out/verify-vet "$proof/verify-vet/main.go" > /out/compile-vet-verifier.txt 2>&1
/out/cost /out/module-closure-cost.json go list -mod=readonly -deps -test -json ./engine/server ./engine/engineutil ./core > /out/module-package-closure.json 2> /out/module-closure.stderr
# The three second-review witnesses first run on exact original c4d01ce native
# owner bytes, against the same new tests and conservative core observation.
# Actual assertion-specific RED is mandatory; setup/compile/timeout is not RED.
cp engine/server/session.go /out/session.current.go
cp engine/server/session_accounting.go /out/session_accounting.current.go
cp /controls/original-c4-session.go engine/server/session.go
cp /controls/original-c4-session_accounting.go engine/server/session_accounting.go
set +e
/out/cost /out/original-c4-cost.json go test -mod=readonly -p=2 -count=1 -timeout=60s -run '^TestSessionAccounting(FailedAuthorizePublishesRemovedBeforeUnlock|PreInitFailureRetainsOnlyExistingRoot|ObservedServicesCannotMintRelease)$' ./engine/server > /out/original-c4.txt 2>&1
c4_exit=$?
set -e
printf '%s\n' "$c4_exit" > /out/original-c4.original-exit
cp /out/session.current.go engine/server/session.go
cp /out/session_accounting.current.go engine/server/session_accounting.go
test "$c4_exit" -eq 1
for witness in FailedAuthorizePublishesRemovedBeforeUnlock PreInitFailureRetainsOnlyExistingRoot ObservedServicesCannotMintRelease; do
 grep -Fq -- "--- FAIL: TestSessionAccounting$witness" /out/original-c4.txt
done
grep -Fq -- 'failed fresh authorization unlocked a reusable session before deletion' /out/original-c4.txt
grep -Fq -- 'failed accounting read leaked the newly created rejected session' /out/original-c4.txt
for variant in retired-success retired-cleanup-error failed-start; do
 grep -Fq -- "[service-cleanup-proof-mismatch test=TestSessionAccountingObservedServicesCannotMintRelease/$variant got=released]" /out/original-c4.txt
done
if grep -Eq -- 'Received unexpected error:|\[build failed\]|panic:|fatal error:|no required module provides|module lookup disabled' /out/original-c4.txt; then exit 1; fi
checkpoint after-original-c4
checkpoint before-server
/out/cost /out/compile-server-cost.json go test -mod=readonly -p=2 -c -o /out/server-accounting.test ./engine/server > /out/compile-server.txt 2>&1
checkpoint after-server
/out/cost /out/compile-executor-cost.json go test -mod=readonly -p=2 -c -o /out/executor-accounting.test ./engine/engineutil > /out/compile-executor.txt 2>&1
checkpoint after-executor
# One additional focused internal core executable, using the same private cache
# and package closure within the unchanged whole-leaf/caller bounds.
/out/cost /out/compile-core-cost.json go test -mod=readonly -p=2 -c -o /out/core-accounting.test ./core > /out/compile-core.txt 2>&1
checkpoint after-core
# Exact reviewed name lists, never a package-wide execution or unrelated tests.
server_pattern=$(cat "$proof/server-tests.pattern")
executor_pattern=$(cat "$proof/executor-tests.pattern")
/out/cost /out/server-tests-cost.json /out/server-accounting.test -test.v -test.count=1 -test.timeout=60s -test.run "$server_pattern" > /out/server-green.txt 2>&1
checkpoint after-server-tests
/out/cost /out/executor-tests-cost.json /out/executor-accounting.test -test.v -test.count=1 -test.timeout=60s -test.run "$executor_pattern" > /out/executor-green.txt 2>&1
checkpoint after-executor-tests
core_pattern=$(cat "$proof/core-tests.pattern")
/out/cost /out/core-tests-cost.json /out/core-accounting.test -test.v -test.count=1 -test.timeout=60s -test.run "$core_pattern" > /out/core-green.txt 2>&1
checkpoint after-core-tests
# Reproduce all three review findings with the prior qualified v4 product
# bytes and the exact same new public witnesses, before running older mutants.
for target in engine/server/session.go engine/server/session_accounting.go engine/engineutil/session_cleanup.go; do
  cp "$target" "/out/$(basename "$target").v5-original"
  cp "/controls/original-qualified-v4/$target" "$target"
done
set +e
/out/cost /out/original-v4-server-cost.json go test -mod=readonly -p=2 -count=1 -timeout=60s -run '^TestSessionAccounting(DisabledInitializedClientTeardown|RetainsEarlierMetricShutdownFailure)$' ./engine/server > /out/original-v4-server.txt 2>&1
original_server=$?
/out/cost /out/original-v4-helper-cost.json go test -mod=readonly -p=2 -count=1 -timeout=60s -run '^TestSessionCleanupAccountingCoversEveryHostServiceHelper$' ./engine/engineutil > /out/original-v4-helper.txt 2>&1
original_helper=$?
set -e
printf '%s\n' "$original_server" > /out/original-v4-server.original-exit
printf '%s\n' "$original_helper" > /out/original-v4-helper.original-exit
for target in engine/server/session.go engine/server/session_accounting.go engine/engineutil/session_cleanup.go; do
  cp "/out/$(basename "$target").v5-original" "$target"
done
test "$original_server" -eq 1 && test "$original_helper" -eq 1
grep -Fq -- '--- FAIL: TestSessionAccountingDisabledInitializedClientTeardown' /out/original-v4-server.txt
grep -Fq -- '--- FAIL: TestSessionAccountingRetainsEarlierMetricShutdownFailure' /out/original-v4-server.txt
grep -Fq -- '--- FAIL: TestSessionCleanupAccountingCoversEveryHostServiceHelper' /out/original-v4-helper.txt
if grep -Eq -- '\[build failed\]|panic:|fatal error:|no required module provides|module lookup disabled' /out/original-v4-server.txt /out/original-v4-helper.txt; then exit 1; fi
checkpoint after-original-v4
# Preserve actual vet RED1 and raw streams. The separate verifier requires all
# four original diagnostics and forbids any new/unparsed source diagnostic.
set +e
/out/cost /out/vet-cost.json go vet -mod=readonly -p=2 ./engine/server ./engine/engineutil > /out/vet.stdout 2> /out/vet.stderr
vet_exit=$?
set -e
printf '%s\n' "$vet_exit" > /out/vet.original-exit
/out/verify-vet "$proof/vet-baseline.json" /src /out/vet.stdout /out/vet.stderr /out/vet-cost.json > /out/vet-comparison.txt 2>&1
checkpoint after-vet
# Only container-private copies change. The immutable host source never changes.
cp engine/server/session_accounting.go /out/session_accounting.original.go
cp engine/engineutil/executor.go /out/executor.original.go
control() {
  name=$1
  target=$2
  package=$3
  pattern=$4
  original=$5
  cp "/controls/$name.go" "$target"
  sha256sum "$target" > "/out/control-$name.sha256"
  set +e
  /out/cost "/out/control-$name-cost.json" go test -mod=readonly -p=2 -count=1 -timeout=60s -run "$pattern" "$package" > "/out/control-$name.txt" 2>&1
  actual=$?
  set -e
  printf '%s\n' "$actual" > "/out/control-$name.original-exit"
  cp "$original" "$target"
  test "$actual" -eq 1
  # An actual named test assertion failure is mandatory. Compile/dependency,
  # timeout or tool failures can never satisfy this negative control.
  grep -Eq -- '--- FAIL: Test(SessionAccounting|SessionCleanupAccounting)' "/out/control-$name.txt"
  if grep -Eq -- '\[build failed\]|panic: test timed out|no required module provides|module lookup disabled' "/out/control-$name.txt"; then exit 1; fi
  checkpoint "after-$name"
}
control release-without-cleanup engine/server/session_accounting.go ./engine/server '^TestSessionAccountingHTTPDurableReleaseAndUncertainCleanup$' /out/session_accounting.original.go
control retained-token-bypass engine/server/session_accounting.go ./engine/server '^TestSessionAccountingHTTPDurableReleaseAndUncertainCleanup$' /out/session_accounting.original.go
control premature-executor-cleanup engine/engineutil/executor.go ./engine/engineutil '^TestSessionCleanupAccountingWaitsThroughCleanupAndPermitRelease$' /out/executor.original.go
checkpoint post
sha256sum /out/server-accounting.test /out/executor-accounting.test /out/core-accounting.test /out/*-cost.json > /out/result.sha256

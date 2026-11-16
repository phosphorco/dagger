#!/bin/sh
# QUEUED: invoke only after the steward grants this exact owner demand.
# This is not a fast path around compilation: every demand compiles its named
# package from source in the same pinned/private/offline container as result().
set -eu
proof=tools/session-accounting-proof
mode=$1
case "$mode" in
  original-mechanisms|corrected-mechanisms) package=./engine/server ;;
  corrected-service-owner) package=./core ;;
  *) exit 2 ;;
esac
pattern=$(cat "$proof/$mode.pattern")
test "$(go env GOVERSION)" = go1.27.0
go version > /out/toolchain.txt
sha256sum go.mod go.sum sdk/go/go.mod sdk/go/go.sum engine/distconsts/go.mod internal/version/VERSION "$proof/focused.sh" "$proof/$mode.pattern" "$proof/cost/main.go" > /out/input-source.sha256
checkpoint() {
  phase=$1
  du -s -B1 /src /out /tmp /go/mod > "/out/scratch-$phase.txt"
  df -B1 --output=avail /src /out /tmp /go/mod > "/out/available-$phase.txt"
  used=$(awk '{n+=$1} END{printf "%.0f",n}' "/out/scratch-$phase.txt")
  test "$used" -le 21474836480
  remaining=$((21474836480-used))
  awk -v required="$remaining" 'NR>1 && $1<required {bad=1} END{exit bad}' "/out/available-$phase.txt"
}
checkpoint pre
if test "$mode" = original-mechanisms; then
  # Only the two original c4d native owner files replace current source.
  # New witness tests and the conservative core observation stay identical.
  cp /controls/original-c4-session.go engine/server/session.go
  cp /controls/original-c4-session_accounting.go engine/server/session_accounting.go
fi
sha256sum engine/server/session.go engine/server/session_accounting.go engine/server/session_accounting_test.go core/services.go core/services_accounting_test.go > /out/owner-test-source.sha256
# Wrapper bootstrap is separately wall-timed; measured compilation below is
# mandatory, not an imported host binary/cache or hidden replacement build.
date -u +%FT%TZ > /out/wrapper-build-start.txt
go build -mod=readonly -trimpath -buildvcs=false -o /out/cost "$proof/cost/main.go"
date -u +%FT%TZ > /out/wrapper-build-end.txt
/out/cost /out/compile-owner-cost.json go test -mod=readonly -p=2 -c -o /out/owner.test "$package" > /out/compile-owner.txt 2>&1
checkpoint after-compile
set +e
/out/cost /out/owner-test-cost.json /out/owner.test -test.v -test.count=1 -test.timeout=60s -test.run "$pattern" > /out/owner-test.txt 2>&1
actual=$?
set -e
printf '%s\n' "$actual" > /out/owner-test.original-exit
if test "$mode" = original-mechanisms; then
  test "$actual" -eq 1
  for witness in FailedAuthorizePublishesRemovedBeforeUnlock PreInitFailureRetainsOnlyExistingRoot ObservedServicesCannotMintRelease; do
    grep -Fq -- "--- FAIL: TestSessionAccounting$witness" /out/owner-test.txt
  done
  grep -Fq -- 'failed fresh authorization unlocked a reusable session before deletion' /out/owner-test.txt
  grep -Fq -- 'failed accounting read leaked the newly created rejected session' /out/owner-test.txt
  for variant in retired-success retired-cleanup-error failed-start; do
    grep -Fq -- "[service-cleanup-proof-mismatch test=TestSessionAccountingObservedServicesCannotMintRelease/$variant got=released]" /out/owner-test.txt
  done
  if grep -Eq -- 'Received unexpected error:|\[build failed\]|panic:|fatal error:|no required module provides|module lookup disabled' /out/owner-test.txt; then exit 1; fi
else
  test "$actual" -eq 0
  case "$mode" in
    corrected-mechanisms) expected=4 ;;
    corrected-service-owner) expected=1 ;;
  esac
  passes=$(grep -c '^--- PASS:' /out/owner-test.txt)
  test "$passes" -eq "$expected"
fi
checkpoint post
sha256sum /out/owner.test /out/*-cost.json > /out/result.sha256

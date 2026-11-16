#!/bin/sh
# Accept only the intended released-versus-uncertain assertion in BOTH cases.
# Fixture errors, a top-level FAIL alone, and missing/wrong states are not RED.
set -eu
log=$1
actual=$2
test "$actual" -eq 1
if grep -Eq -- 'cloud-control-fixture-failure|Received unexpected error:|\[build failed\]|panic:|fatal error:|no required module provides|module lookup disabled' "$log"; then
  echo 'Cloud control rejected: fixture/acquisition/tool failure' >&2
  exit 1
fi
name=TestSessionAccountingRetainedCloudCannotMintRelease
grep -Eq -- "^--- FAIL: $name \([0-9.]+s\)$" "$log"
for variant in successful-exporter-shutdown failed-exporter-shutdown; do
  grep -Fq -- "--- FAIL: $name/$variant (" "$log"
  # Testify prints the message only on assertion failure. End anchoring forbids
  # released-prefix states; each marker carries its exact subtest identity.
  grep -Eq -- "\[cloud-release-state-mismatch test=$name/$variant want=cleanup-uncertain got=released\]$" "$log"
done

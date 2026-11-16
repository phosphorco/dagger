#!/bin/sh
# Host diagnostic of a log discriminator, never native runtime qualification.
# Generate genuine Go test FAIL/PASS output; do not substitute canned log text.
set -eu
go_bin=$1
verifier=$2
out=$3
mkdir "$out"
out=$(cd "$out" && pwd)
mkdir "$out/fixture"
cat > "$out/fixture/cloud_control_test.go" <<'GO'
package diagnostic
import ("flag"; "testing")
var scenario = flag.String("fixture.case", "", "diagnostic failure mode")
func TestSessionAccountingRetainedCloudCannotMintRelease(t *testing.T) {
 for _, variant := range []string{"successful-exporter-shutdown", "failed-exporter-shutdown"} {
  t.Run(variant, func(t *testing.T) {
   switch *scenario {
   case "acquisition": t.Fatal("cloud-control-fixture-failure: acquire forwarder")
   case "status": t.Fatal("cloud-control-fixture-failure: status unavailable")
   case "mixed":
    if variant == "failed-exporter-shutdown" {t.Fatal("cloud-control-fixture-failure: acquire forwarder")}
   case "unexpected-error": t.Fatal("Error: Received unexpected error: store open failed")
   case "cleanup-error": t.Cleanup(func(){t.Error("Error: Received unexpected error: cleanup failed")})
   }
   got := "released"
   if *scenario == "wrong-state" {got="unknown"}
   if *scenario == "prefix-state" {got="released-but-unproved"}
   if *scenario == "marker-with-pass" {
    t.Logf("[cloud-release-state-mismatch test=%s want=cleanup-uncertain got=%s]",t.Name(),got)
    return
   }
   t.Fatalf("[cloud-release-state-mismatch test=%s want=cleanup-uncertain got=%s]",t.Name(),got)
  })
 }
}
GO
# The fixture has only stdlib imports and lives outside the native module.
(cd "$out/fixture" && "$go_bin" test -c -o "$out/fixture.test" cloud_control_test.go) > "$out/compile.txt" 2>&1
failed=0
for scenario in state acquisition status mixed unexpected-error cleanup-error wrong-state prefix-state marker-with-pass; do
  set +e
  "$out/fixture.test" -test.v -test.count=1 -test.timeout=5s -fixture.case="$scenario" > "$out/$scenario.txt" 2>&1
  original=$?
  sh "$verifier" "$out/$scenario.txt" "$original" > "$out/$scenario-verifier.txt" 2>&1
  verdict=$?
  set -e
  printf '%s\n' "$original" > "$out/$scenario.original-exit"
  printf '%s\n' "$verdict" > "$out/$scenario.verifier-exit"
  if test "$scenario" = state; then expected=0; else expected=1; fi
  # Also pin the genuine fixture outcome; crashes or compilation cannot stand
  # in for the expected named assertion failures.
  if test "$scenario" = marker-with-pass; then wanted_original=0; else wanted_original=1; fi
  if test "$original" -ne "$wanted_original" || test "$verdict" -ne "$expected"; then
    echo "FAIL cloud-control/$scenario: fixture=$original verifier=$verdict expected=$expected"
    failed=1
  else
    echo "PASS cloud-control/$scenario: fixture=$original verifier=$verdict"
  fi
done
exit "$failed"

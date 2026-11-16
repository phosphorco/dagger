# Native accounting closure boundaries for Simple phases 1–3

This follows the reviewed optional owner in PR1 at c4d01ce. It adds a Cloud
refusal fixture and records missing inputs; it supplies no runtime capability,
new producer, protocol, issuer, controller, activation or resource policy.
Accounting and gateway enforcement remain off. PR1's completed review stays
attached to its exact source. Heavy-leaf ready/permit instrumentation is a
separate owner's work and is not changed here.

## What source establishes

The native session owner authenticates the existing root/client credential,
retains immutable engine/session generation and intent binding, and records
cleanup/reconciliation/acknowledgement in its explicit bounded store. Existing
request/cache/client-scope/service/agent barriers supply separate cleanup facts.
Unknown helper coverage, retained Cloud forwarding and any cleanup error refuse
a release receipt. The record and shadow phases can retain those unknowns;
they cannot treat them as measured zero, a complete census or resource authority.

The executor fixture exercises the real cleanup/permit defer with fake process
steps. The positive release fixture has an explicitly empty executor domain.
Git SSH/capture/apply negatives reach an injected caller resolver before any
transport, agent, credential or external work. These facts do not establish
positive cleanup for actual SSH agents, services or arbitrary client helpers.

## Remaining inputs and the existing owner that must provide them

| Boundary | Source fact | What would close it; current disposition |
| --- | --- | --- |
| Client host-service helpers | `engine/engineutil/session_cleanup.go` observes uncertainty at `Client.GetHostServiceCaller`. `engine/session/git/git.proto` has request/stream operations but no terminal resource-cleanup acknowledgement. `git_push_ssh.go` may keep one agent per destination; its local `Close` kills/reaps owned agents but ignores kill/remove errors and does not attest anything back to the engine. A supplied `SSH_AUTH_SOCK` belongs to the user and must not be terminated. | The existing client/helper owner must distinguish ownership/retained resources and report terminal outcome for the exact session/client generation, including descendants and failures. A returned RPC/EOF/socket path or client disconnect is insufficient. This needs an actual owner protocol and pinned client compatibility, followed by real-component evidence; no positive capability is manufactured in this followup. |
| Native helper resources | `engine/engineutil/executor.go` marks direct `Exec` and network namespaces uncovered. A namespace can outlive its constructor until `Release`; deleting a running-map entry is not its terminal guarantee. | Each existing resource owner must attest its own release, errors and relevant permit/descendant completion. A real native namespace/process proof needs the separately commissioned runtime and isolation context. The current owner conservatively keeps uncertainty; no helper lifecycle is replaced. |
| Retained Cloud resources | `session_cloud_forwarder.go:finish` schedules asynchronous work. `release` logs exporter/DB close errors and then closes `done`; `waitReleased` only waits for that channel. `session_cloud_telemetry.go` owns exporter/token/queue setup. | A successful `waitReleased` does not retain a cleanup result or attest a disjoint shared-domain transfer. The current session owner refuses a receipt whenever a forwarder was retained. Closing this requires outcome retention by that same owner and either positive terminal resource evidence or an enumerated, accounted retained/shared transfer. Do not block/stop forwarding or change Cloud/service lifetime just to obtain a receipt. No Cloud credentials or live activation are requested here. |
| Final usage availability | `engine/engineutil/exec_usage.go:recordExecUsage` is profiling-dependent and omits unsupported/unreadable cgroups; mount-helper join failure makes readings unavailable. Final attributes are not an all-domain session census. | The existing exec/helper accounting owners must carry exact scope identity, units, terminal/availability state and inclusive/disjoint relation. Present observations remain partial and censored. No profile-on default or zero fallback is introduced. |
| Usage export and retention | `engine/telemetry/workload_readings.go` admits CPU/current-memory/network readings, not final kernel peak; overflow increments an internal dropped count. `workload_spans.go:workloadSpanAttribute` omits `wcprof.usage.*` and only exports ended engine spans. `workload_export.go` owns bounded queues. | Native accounting, exporter and retained-query owners must agree an end-to-end coverage revision, finality and drop/availability representation. Adding a local field or widening an allowlist alone cannot establish retained completeness. Unfinished/missing usage stays unknown; ancestor capacity is never summed with descendants. |
| Worker eligibility and shared allowance | This optional session store does not authenticate installed worker/container release, actual quota/arch/trust/config or a disjoint shared allowance. | The privileged capacity/lifecycle owner must supply exact operative receipts and supported shared-domain scope/allowance. Source declarations or CLI Info are not operative proof. This requires authority outside the native session source owner; return it through the platform steward. |
| Protected admission and adapter | Existing native credentials prove native root/ancestry, not verified Tailnet CI class, source/workload policy, protected ingress headroom or gateway atomic send ownership. No live `SafetyProofSource` adapter ships here. | Existing ingress/policy/gateway owners must bind those inputs and qualify wire/cancellation/total limits before M_g. Observation and shadow proceed with honest absent inputs. No request-blocking path or selected CI shape is authorized by this fixture. |

## Cloud refusal discriminator

`TestSessionAccountingRetainedCloudCannotMintRelease` starts the existing native
forwarder with in-process exporters and its real private client store, then runs
the existing session teardown. It observes `finish` while exporter shutdown is
held, and checks public authenticated status has no release receipt. It then
allows exporter shutdown to succeed or fail, waits for the actual forwarder
owner to release, and checks the same durable uncertainty after registry
retirement and accounting-store reopen. A fabricated Cloud-finished ack is denied.

The existing release-without-cleanup mutant must fail this exact named assertion.
The positive no-Cloud/empty-domain fixture remains in the unchanged test set.
The fixture proves refusal semantics only; it does not establish a shared Cloud
resource envelope, a real exporter/SSH-agent cleanup guarantee or live authority.

Simple phases 1–3 can finish their source/observation/shadow work with these
explicit unknown inputs. A claim of complete producer closure or enforced M_g
cannot. The remaining work crosses the named owner/protocol and operative-proof
boundaries above; repeating synthetic proofs does not close it.

## Current qualification limit

One invocation of the existing offline shared-native entry returned caller exit1
in43.51s with HTTP502: selected engine b8-hel was unavailable after the session
attachables connection closed. No result Directory or leaf exit was returned.
Trace `4f1dcd02332f1674919fa36a01966cf5`, session
`K2W3XT7LB3AJCJAA7QZLDOF4ME`, and the raw caller failure are retained. This is an
unavailable qualification, not a named assertion failure or evidence that no
execution occurred. The new Cloud fixture/mutant assertion and current vet were
not qualified by that call. PR1's completed review and earlier28-case proof stay
scoped to c4d01ce; neither is inherited as GREEN for this followup. No unchanged
retry, budget increase, alternate route or live operation is introduced.

The PR2 discriminator correction requires an assertion-specific
`cloud-release-state-mismatch` message naming the exact exporter subtest and
`want=cleanup-uncertain got=released` in **both** shutdown variants, plus their
actual subtest failures and exit1. Explicit fixture/acquisition errors are
rejected. Setup failure and top-level test failure alone cannot qualify RED.
A bounded host stdlib Go diagnostic generated genuine test output for nine
cases. The original discriminator failed the diagnostic by accepting acquisition,
status, mixed, unexpected-error, cleanup-error and wrong/prefix-state failures;
the corrected discriminator passed all nine cases. This qualifies the log
reader only: the original502 result and unavailable native fixture/vet claim
above remain unchanged, with no additional native product call.

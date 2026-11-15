# Session accounting producer: off-by-default source

This source is off by default. Focused test executables have been compiled and run; no
engine executable/image has been built or deployed. No cmd flag,
production Config, listener, credential issuer, deployment or gateway adapter is
introduced. The host must explicitly supply a SessionAccountingStore capability
to NewServerOpts; existing nil behavior remains outside this guarantee. Source
was introduced from actual upstream f0a3d728, authenticated against the pinned
archive, with accepted d866 and VERSION6 reproduced before any new edits. This
candidate must never be packaged/published as the immutable phosphor.6 release.
Independent source/result review and a separately authorized release proposal
remain required. This draft grants no runtime activation.

The existing Server/session/client lifecycle owns authorization, initialization,
ancestry, closing and cleanup. A bounded native bbolt store retains only their
original binding, credential verifier, sequence, domain outcome and ack facts.
It is neither a copied session controller nor a dispatch/replay queue. Native
credentials stay in the native owner's private store; no verifier or token is
returned in DTOs, diagnostics or the gateway. The original client chooses its
credential under the existing bootstrap protocol; no credential is minted here.

Explicit fresh/open operations require an existing owner-private directory.
Fresh creation requires emptiness and exclusive database creation; Open never
creates or adopts missing/empty/foreign files. A scoped os.Root and bbolt's
existing OpenFile option retain the actual directory/file identity and prevent
symlink-following/recreation. Native bbolt's syncs stay enabled; initial parent
sync is explicit. A lost storage identity or ambiguous commit inhibits native
proof/ack until explicit owner recovery. Rejected credentials/conflicting DTOs
and retention-full errors roll back without poisoning otherwise healthy facts.
Records, logical bytes and physical file bytes are finite explicit constructor
inputs. The pinned native bbolt already exposes MaxSize; its data-file growth
limit is used directly, with no profile/cache/schema ownership changes. Acknowledged
facts and uncertainty never expire. No filesystem or production allocation is
selected. Bbolt overhead/mapping/temporary costs and the remaining filesystem headroom
still need actual measurement through the existing lifecycle owner.

The existing Server.ServeHTTP boundary handles the bounded POST operations
/v1/session-accounting/{authorize,bind,ancestry,status,ack} before getOrInitClient
can initialize resources. There is no second network server. Authorize stages
ordinary existing uninitialized session/client records, validates existing-root
credentials before setup, and returns the native generation. A fresh root has
no previously stored secret: this is bounded native registration under the
existing owner, not a claim that the native token authenticates a CI principal.
Bind attaches one immutable gateway hold/first-intent sequence after the gateway
has persisted it. Native generation/root/session/epoch/descriptor bindings never
change or resurrect a terminal generation. Existing unaccounted running roots
are not silently enrolled. Legitimate registered nested ancestry comes from
actual records, token and native transport handles; it grants no class upgrade.

The descriptor is an immutable designation. Native echoing/binding it does NOT
authorize a cheaper workload kind. Protected ingress principal/class and the
approved source/kind policy remain separate required producers. Worker quota,
release/architecture/trust/config eligibility and shared allowance are also
separate privileged inputs. No production SafetyProofSource adapter ships here.
Legacy/unaccounted native callers remain outside this accounting guarantee;
unknown outside-domain allowance still blocks future enforced-fit claims.

Status is an exact authenticated read, with monotonic native event sequence,
explicit unknown/closing/running/cleanup-uncertain/released state and an optional
positive durable receipt. Absence of a native map entry or an old process's
nonterminal record becomes unknown. Existing initialization/closing transitions
produce durable facts; only receipt+generation+intent can release gateway debit.
Terminal native facts survive map retirement and ordinary OTLP loss. Native ack
is exact and idempotent; native retention preserves its credential authority and
tombstone, including repeat ack after a gateway's lost acknowledgement.

removeDaggerSession's existing service/agent/request/cache/scope barriers feed
separate outcomes. Failed init/shutdown retain uncertainty. Any cleanup error,
unobserved domain, failed native persistence or telemetry-provider shutdown
failure yields no positive receipt; the original errors stay visible. The
wcprof completeness carrier is never consulted as a cleanup oracle. Mere loss
of exported delivery after successful native barriers cannot erase the durable
outcome, because reconciliation reads the native store rather than OTLP.

The existing per-session engineutil.Client uses its closeMu for a small cleanup
summary. The new observation starts outside the existing run lifecycle defer
and ends only AFTER its original cleanup and semaphore release, including failed
cleanup. No permit, N2/flags, process cancellation, cache or service lifecycle is
replaced. Executable scope/cache teardown precedes sealing new resource starts.

**Remaining coverage limitation:** many helper APIs lack a positive terminal
resource/error oracle. Calling them marks coverage unknown, deliberately making
release unavailable even if ordinary telemetry says complete. Direct Exec and
network namespaces are similarly conservative. Existing Cloud forwarding that
intentionally survives the session also requires an actual accounted shared
transfer; its mere done/finished marker is not substituted for that proof. The prepared positive source
control has a real native teardown with an explicitly empty executor domain;
it is not proof for arbitrary user helpers. Further helper-owner extensions or
explicit positive retained/shared transfers must be reviewed before affected
real workloads can produce a complete release. No boolean configuration can
assert those missing proofs and no helper failure is zero-filled.

Prepared controls use Server.ServeHTTP and the actual native durable store,
request/cache/scope/service teardown, retained auth, generation conflicts,
registry retirement/reopen, missing storage, false completeness, bounded input,
retention and idempotent ack. Executor controls use the existing canonical
no-child test fixture with the actual cleanup/permit defer chain; synthetic
process steps are labelled as such. The initial v3 qualification compiled both packages but
failed seven public HTTP cases: the legacy hijack probe committed200 before error
status. Its original source, executables, failure logs and costs remain retained.
The correction confines response ownership to the new nonhijacking accounting
operations and typed accounting pre-initialization rejection. It reuses the existing
httpError writer before commit; legacy GraphQL/hijacking/error behavior is unchanged.
No assertion, input or deadline was weakened.

One offline Go1.27 v4 proof then completed caller/leaf0 with all17selected server
cases and8engineutil cases PASS. Three native-source mutants fail actual named
assertions: missing cleanup guard, retained credential bypass, and premature exec
completion before cleanup/permit release. Actual go vet remains RED1 with exactly
the four original lost-cancel diagnostics; a separate comparison matched their
function/expression/column/message identities. Vet was not disabled or relabelled
green. Positive cleanup coverage is the explicitly empty-executor control; fake
process fixtures do not establish arbitrary real helper/Cloud cleanup.

The proof used the existing publishedclient4/shared router and pinned Go1.27image
4013ae0f, offline module SOURCE and private derived cache, with the existing
600s+5leaf/900s+5caller and60s test deadlines. Trace
a6ab34426f7b08aa1b4ed73a8f6b88b9 binds sessionA5TBAWOFHNBY4F6AAZ6G2FM75P,
b2-hel/container7860f9f6a2507f1937152dd8fb2b449a67e8214f28532a9854e326313c1ab84b,
instancee4e46cad-ead5-4909-9a36-bf2c5c4fc398/native6 and
cache d847599f-8350-4c80-a0af-fc06eb2d26b5/4. One leaf measured363.354461s,
inclusiveCPU925.595641s/kernel peak6341955584bytes; caller404.929006s. Largest
observed own-scratch checkpoint4705304576bytes and minimum available684559147008
bytes passed the declared20GiB checkpoint stop/free-space guard. This is no disk
quota or between-checkpoint peak claim. GOMAX2/GOMEM2GiB/-p2 are soft settings.
Per-command wait4 costs and source/module/control/exit bindings are retained.

This qualifies focused owner contracts, not a live native release receipt, full
helper/Cloud coverage, protected ingress/root/class/eligibility, gateway adapter,
CI allowance, production cost or M_g enforcement. Telemetry completeness remains
unknown; the successful proof carrier is not cleanup authority.

Authorized-but-unbound bootstrap facts also retain native ID/credential authority
within the explicit finite count/byte bounds. They cannot later dispatch without
a live authenticated native request and durable gateway intent. This cut provides
no generic TTL/absence cleanup or silent tombstone compaction; exhausted native
retention is explicit unavailability pending a separately reviewed retirement or
incident-resolution operation. That operational cost is not hidden as progress.

The optional cleanup confirmation is required only for a session with both an
accounting store and an accounted binding. Ordinary initialized executor clients
keep their existing teardown behavior. Each stable native client record retains
the first actual metric-shutdown failure; provider disposal and quiescent runtime
reclamation cannot erase that failure before accounting finalization. The
retained error is joined only into the optional accounting outcome.

Host-service helper uncertainty is observed at the existing Client caller-routing
boundary, including Git SSH auth, capture and apply. The one resolver remains in
Opts. No new transport, helper lifecycle, or positive terminal guarantee is
introduced; any observed helper remains uncertain until its owner supplies one.
The focused review correction adds disabled teardown and prior-shutdown/reclaim
witnesses, plus each of the three omitted helpers, keeping all original tests,
assertions and 600/900-second proof bounds.


The review correction's v5 offline shared proof completed caller/leaf0 with
19 selected server cases and9engineutil cases PASS (all original25 plus the three
focused witnesses). The same new witnesses against unchanged qualified-v4 product
bytes failed actual named assertions for disabled-accounting teardown, both prior
metric-shutdown/reclamation paths, and each Git SSH/capture/apply helper. The
three original behavioral mutants also remained actual assertionREDs. Vet stays
actualRED1 with the same four baseline diagnostic identities; no assertion,
selection, deadline, cancellation or execution budget was weakened.

Traceb29ecbe111ed69de69c50511b90a2b82 binds sessionI4CYJQHH3MEVEG6X56BS6CXWWY,
b2-hel/container7860f9f6a2507f1937152dd8fb2b449a67e8214f28532a9854e326313c1ab84b,
instancee4e46cad-ead5-4909-9a36-bf2c5c4fc398/native6 and
cache d847599f-8350-4c80-a0af-fc06eb2d26b5/4. One proof leaf measured296.954809s,
inclusiveCPU719.953691s and kernel peak6202724352bytes; caller314.567796s.
Largest observed own-scratch checkpoint5350891520bytes and minimum available
674057240576bytes passed the unchanged20GiB checkpoint stop/free-space guard.
These are source-qualification costs including compilation, not live gateway or
engine workload cost. Soft settings, unknown transient peaks/delivery and absent
live authoritative cleanup receipt retain the limits described above. Metric
provider failure fixtures call the real shutdown/scope owners and public durable
status path; Git negatives stop at the injected native caller resolver before
transport. They do not assert real SSH-agent or Cloud terminal cleanup.

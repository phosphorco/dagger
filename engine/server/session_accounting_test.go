package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

func nativeAccountingFixture(t *testing.T) (*Server, *SessionAccountingStore, string, *engine.ClientMetadata) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0700))
	store, err := CreateSessionAccountingStore(dir, SessionAccountingLimits{Records: 8, RecordBytes: 16 << 10, LogicalBytes: 256 << 10, PhysicalBytes: 1 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	srv := newTeardownTestServer(t)
	srv.engineInstanceID = "native-epoch"
	srv.sessionAccounting = store
	md := &engine.ClientMetadata{SessionID: "session", ClientID: "root", ClientSecretToken: "synthetic-native-root-secret", ClientVersion: engine.Version}
	return srv, store, dir, md
}
func nativeAccountingRequest(t *testing.T, srv *Server, md *engine.ClientMetadata, op string, binding SessionAccountingBinding, receipt *NativeSessionReleaseReceipt) (*httptest.ResponseRecorder, SessionAccountingSnapshot) {
	t.Helper()
	input := map[string]any{"binding": binding}
	if receipt != nil {
		input["receiptID"] = receipt.ReceiptID
		input["nativeSequence"] = receipt.NativeSequence
	}
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, sessionAccountingPrefix+op, bytes.NewReader(raw))
	req.Header = md.AppendToHTTPHeaders(req.Header)
	response := httptest.NewRecorder()
	srv.ServeHTTP(response, req)
	var result SessionAccountingSnapshot
	if response.Code == http.StatusOK && op != "ancestry" {
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	}
	require.NotContains(t, response.Body.String(), md.ClientSecretToken)
	digest := sha256TokenForTest(md.ClientSecretToken)
	require.NotContains(t, response.Body.String(), digest)
	require.NotContains(t, response.Body.String(), "rootVerifier")
	return response, result
}
func sha256TokenForTest(token string) string {
	// Test-only verifier formatting is used solely as a response leak oracle.
	got := sha256.Sum256([]byte(token))
	return hex.EncodeToString(got[:])
}
func nativeAuthorizeAndBind(t *testing.T, srv *Server, md *engine.ClientMetadata) SessionAccountingBinding {
	t.Helper()
	binding := SessionAccountingBinding{EngineEpoch: srv.engineInstanceID, SessionID: md.SessionID, RootClientID: md.ClientID, DescriptorRef: "policy-owned-descriptor"}
	response, authorized := nativeAccountingRequest(t, srv, md, "authorize", binding, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NotZero(t, authorized.Binding.Generation)
	binding = authorized.Binding
	binding.HoldID = "durable-gateway-hold"
	binding.DispatchSequence = 7
	response, bound := nativeAccountingRequest(t, srv, md, "bind", binding, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, binding, bound.Binding)
	return binding
}

func TestSessionAccountingHTTPAuthBeforeResourceSetup(t *testing.T) {
	srv, _, _, md := nativeAccountingFixture(t)
	binding := SessionAccountingBinding{EngineEpoch: srv.engineInstanceID, SessionID: md.SessionID, RootClientID: md.ClientID, DescriptorRef: "policy-owned-descriptor"}
	response, result := nativeAccountingRequest(t, srv, md, "authorize", binding, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	// This server has no engine/runtime initialization dependencies. The public
	// control must succeed entirely through the existing bootstrap identity owner.
	response, status := nativeAccountingRequest(t, srv, md, "status", result.Binding, nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "unknown", status.State)
	bad := *md
	bad.ClientSecretToken = "wrong-native-secret"
	response, _ = nativeAccountingRequest(t, srv, &bad, "authorize", binding, nil)
	require.Equal(t, http.StatusConflict, response.Code)
	response, _ = nativeAccountingRequest(t, srv, md, "authorize", binding, nil)
	require.Equal(t, http.StatusOK, response.Code, "rejected auth must not poison valid storage")
	// No intent means native initialization must fail before it can touch missing
	// runtime dependencies. A false implementation would panic/do setup here.
	req := httptest.NewRequest(http.MethodPost, engine.InitEndpoint, strings.NewReader("{}"))
	req.Header = md.AppendToHTTPHeaders(req.Header)
	out := httptest.NewRecorder()
	srv.ServeHTTP(out, req)
	require.NotEqual(t, http.StatusOK, out.Code)
	response, status = nativeAccountingRequest(t, srv, md, "status", result.Binding, nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "unknown", status.State)
	bad = *md
	bad.ClientID = "same-session-different-root"
	response, _ = nativeAccountingRequest(t, srv, &bad, "authorize", SessionAccountingBinding{EngineEpoch: srv.engineInstanceID, SessionID: md.SessionID, RootClientID: bad.ClientID, DescriptorRef: binding.DescriptorRef}, nil)
	require.NotEqual(t, http.StatusOK, response.Code)
}

// Reuse the native teardown fixtures and real services/cache/scope/telemetry
// owners. Only runtime/process startup is omitted: the positive case has an
// explicitly empty executor domain, not a fake successful process cleanup.
func accountingTeardownSession(t *testing.T, srv *Server, md *engine.ClientMetadata, b SessionAccountingBinding, fail bool) *daggerSession {
	t.Helper()
	sess := srv.daggerSessions[md.SessionID]
	sess.accountingBinding = b
	sess.services = core.NewServices()
	sess.analytics = analytics.New(analytics.Config{DoNotTrack: true})
	if fail {
		sess.analytics = failingCloseTracker{}
	}
	sess.shutdownCh = make(chan struct{})
	sess.closingCtx, sess.cancelClosing = context.WithCancelCause(context.Background())
	sess.dagqlCond = sync.NewCond(&sess.dagqlMu)
	srv.clientDBs = clientdb.NewDBs(t.TempDir())
	srv.telemetryPubSub = NewPubSub(srv)
	sess.telemetryPubSub = srv.telemetryPubSub
	srv.initializeSessionTelemetry(sess, nil)
	sess.state.Store(sessionStateInitialized)
	require.NoError(t, srv.markSessionAccountingState(sess, "initialized"))
	return sess
}
func TestSessionAccountingHTTPDurableReleaseAndUncertainCleanup(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "positive-empty-domain", true: "cleanup-error-even-after-complete"}[failed], func(t *testing.T) {
			srv, store, dir, md := nativeAccountingFixture(t)
			binding := nativeAuthorizeAndBind(t, srv, md)
			sess := accountingTeardownSession(t, srv, md, binding, failed)
			sess.lifecycleMu.Lock()
			err := srv.removeDaggerSession(t.Context(), sess)
			sess.lifecycleMu.Unlock()
			srv.retireSession(sess)
			if failed {
				require.ErrorContains(t, err, "analytics close failed")
			} else {
				require.NoError(t, err)
			}
			response, status := nativeAccountingRequest(t, srv, md, "status", binding, nil)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			if failed {
				require.Equal(t, "cleanup-uncertain", status.State)
				require.Nil(t, status.Receipt)
				require.Contains(t, status.CleanupError, "analytics close failed")
			} else {
				require.Equal(t, "released", status.State)
				require.NotNil(t, status.Receipt)
			}
			// The native registry has retired. Retained native credential authority and
			// receipt remain readable independently of telemetry or map membership.
			require.NoError(t, store.Close())
			recovered, err := OpenSessionAccountingStore(dir, store.limits)
			require.NoError(t, err)
			defer recovered.Close()
			srv.sessionAccounting = recovered
			srv.engineInstanceID = "replacement-native-epoch"
			srv.daggerSessions = map[string]*daggerSession{}
			response, replayed := nativeAccountingRequest(t, srv, md, "status", binding, nil)
			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, status, replayed)
			if !failed {
				wrong := *md
				wrong.ClientSecretToken = "wrong-ack-credential"
				response, _ = nativeAccountingRequest(t, srv, &wrong, "ack", binding, status.Receipt)
				require.Equal(t, http.StatusUnauthorized, response.Code)
				response, acked := nativeAccountingRequest(t, srv, md, "ack", binding, status.Receipt)
				require.Equal(t, http.StatusOK, response.Code)
				require.True(t, acked.Acked)
				response, again := nativeAccountingRequest(t, srv, md, "ack", binding, status.Receipt)
				require.Equal(t, http.StatusOK, response.Code)
				require.Equal(t, acked, again)
			}
		})
	}
}
func TestSessionAccountingMissingAndFalseCompletenessNeverRelease(t *testing.T) {
	srv, _, _, md := nativeAccountingFixture(t)
	binding := nativeAuthorizeAndBind(t, srv, md)
	// Simulate native registry loss/completeness without ANY positive native
	// teardown. The actual control response must be unknown and carry no receipt.
	srv.daggerSessions = map[string]*daggerSession{}
	response, status := nativeAccountingRequest(t, srv, md, "status", binding, nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "unknown", status.State)
	require.Nil(t, status.Receipt)
	response, _ = nativeAccountingRequest(t, srv, md, "ack", binding, &NativeSessionReleaseReceipt{ReceiptID: "trace-complete", NativeSequence: 1})
	require.NotEqual(t, http.StatusOK, response.Code)
	wrong := binding
	wrong.Generation++
	response, _ = nativeAccountingRequest(t, srv, md, "status", wrong, nil)
	require.NotEqual(t, http.StatusOK, response.Code)
	// Rejoin from another trusted root/client still needs native registered ancestry.
	child := *md
	child.ClientID = "unregistered-child"
	response, _ = nativeAccountingRequest(t, srv, &child, "ancestry", binding, nil)
	require.Equal(t, http.StatusUnauthorized, response.Code)
}
func TestSessionAccountingLostStorageDoesNotRecreateOrAck(t *testing.T) {
	srv, _, dir, md := nativeAccountingFixture(t)
	binding := nativeAuthorizeAndBind(t, srv, md)
	path := filepath.Join(dir, "session-accounting.db")
	require.NoError(t, os.Rename(path, path+".retained"))
	response, _ := nativeAccountingRequest(t, srv, md, "status", binding, nil)
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	_, err := OpenSessionAccountingStore(dir, SessionAccountingLimits{Records: 8, RecordBytes: 16 << 10, LogicalBytes: 256 << 10, PhysicalBytes: 1 << 20})
	require.Error(t, err)
	_, err = CreateSessionAccountingStore(dir, SessionAccountingLimits{Records: 8, RecordBytes: 16 << 10, LogicalBytes: 256 << 10, PhysicalBytes: 1 << 20})
	require.Error(t, err)
	_, err = os.Stat(path)
	require.True(t, errors.Is(err, os.ErrNotExist))
}
func TestSessionAccountingDisabledAndBoundedControls(t *testing.T) {
	srv, store, _, md := nativeAccountingFixture(t)
	srv.sessionAccounting = nil
	response, _ := nativeAccountingRequest(t, srv, md, "authorize", SessionAccountingBinding{}, nil)
	require.Equal(t, http.StatusNotFound, response.Code)
	srv.sessionAccounting = store
	req := httptest.NewRequest(http.MethodPost, sessionAccountingPrefix+"authorize", strings.NewReader(strings.Repeat("x", 17<<10)))
	req.Header = md.AppendToHTTPHeaders(req.Header)
	out := httptest.NewRecorder()
	srv.ServeHTTP(out, req)
	require.Equal(t, http.StatusBadRequest, out.Code)
}

func TestSessionAccountingRetentionPreservesPriorFacts(t *testing.T) {
	srv, store, _, md := nativeAccountingFixture(t)
	binding := nativeAuthorizeAndBind(t, srv, md)
	for i := 1; i < 9; i++ {
		other := *md
		other.SessionID = string(rune('a'+i)) + "-session"
		response, _ := nativeAccountingRequest(t, srv, &other, "authorize", SessionAccountingBinding{EngineEpoch: srv.engineInstanceID, SessionID: other.SessionID, RootClientID: other.ClientID, DescriptorRef: "policy-owned-descriptor"}, nil)
		if i < 8 {
			require.Equal(t, http.StatusOK, response.Code)
		} else {
			require.NotEqual(t, http.StatusOK, response.Code)
		}
	}
	response, status := nativeAccountingRequest(t, srv, md, "status", binding, nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.Nil(t, status.Receipt)
	require.False(t, store.fault.Load(), "retention refusal is not an ambiguous disk write")
}

func TestSessionAccountingUnobservedExecutorDomainCannotRelease(t *testing.T) {
	srv, _, _, md := nativeAccountingFixture(t)
	binding := nativeAuthorizeAndBind(t, srv, md)
	sess := accountingTeardownSession(t, srv, md, binding, false)
	// An executor client with no actual coverage installation cannot be inferred
	// empty from missing running rows, span counts, or successful cache teardown.
	sess.engineUtilClient = &engineutil.Client{}
	sess.lifecycleMu.Lock()
	err := srv.removeDaggerSession(t.Context(), sess)
	sess.lifecycleMu.Unlock()
	srv.retireSession(sess)
	require.ErrorContains(t, err, "cleanup coverage unavailable")
	response, status := nativeAccountingRequest(t, srv, md, "status", binding, nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "cleanup-uncertain", status.State)
	require.Nil(t, status.Receipt)
}

func TestSessionAccountingMissingCredentialsDoNotCreateState(t *testing.T) {
	srv, _, _, _ := nativeAccountingFixture(t)
	req := httptest.NewRequest(http.MethodPost, sessionAccountingPrefix+"authorize", strings.NewReader("{}"))
	out := httptest.NewRecorder()
	srv.ServeHTTP(out, req)
	require.Equal(t, http.StatusBadRequest, out.Code)
}

func TestSessionAccountingStatusDoesNotWaitForLifecycleOwner(t *testing.T) {
	for _, state := range []string{"initialized", "closing"} {
		t.Run(state, func(t *testing.T) {
			srv, _, _, md := nativeAccountingFixture(t)
			binding := nativeAuthorizeAndBind(t, srv, md)
			sess := accountingTeardownSession(t, srv, md, binding, false)
			if state == "closing" {
				require.NoError(t, srv.markSessionAccountingState(sess, "closing"))
			}
			sess.lifecycleMu.Lock()
			defer sess.lifecycleMu.Unlock()
			raw, err := json.Marshal(map[string]any{"binding": binding})
			require.NoError(t, err)
			request := httptest.NewRequest(http.MethodPost, sessionAccountingPrefix+"status", bytes.NewReader(raw))
			request.Header = md.AppendToHTTPHeaders(request.Header)
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); srv.ServeHTTP(response, request) }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("reconciliation waited for native lifecycle owner")
			}
			require.Equal(t, http.StatusOK, response.Code)
			var snapshot SessionAccountingSnapshot
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &snapshot))
			require.Nil(t, snapshot.Receipt)
			if state == "closing" {
				require.Equal(t, "closing", snapshot.State)
				require.True(t, snapshot.Complete)
			} else {
				require.Equal(t, "unknown", snapshot.State)
				require.False(t, snapshot.Complete)
			}
			for _, op := range []string{"authorize", "ancestry"} {
				req := httptest.NewRequest(http.MethodPost, sessionAccountingPrefix+op, bytes.NewReader(raw))
				req.Header = md.AppendToHTTPHeaders(req.Header)
				resp := httptest.NewRecorder()
				returned := make(chan struct{})
				go func() { defer close(returned); srv.ServeHTTP(resp, req) }()
				select {
				case <-returned:
				case <-time.After(time.Second):
					t.Fatalf("%s waited for native lifecycle owner", op)
				}
				require.Equal(t, http.StatusServiceUnavailable, resp.Code)
			}
		})
	}
}

func TestSessionAccountingDisabledInitializedClientTeardown(t *testing.T) {
	srv := newTeardownTestServer(t)
	srv.clientDBs = clientdb.NewDBs(t.TempDir())
	srv.telemetryPubSub = NewPubSub(srv)
	sess, _ := newTeardownTestSession(srv, "ordinary-disabled", "root", 0)
	sess.telemetryPubSub = srv.telemetryPubSub
	srv.initializeSessionTelemetry(sess, nil)
	releaseTeardownDrain(sess)
	var err error
	sess.engineUtilClient, err = engineutil.NewClient(t.Context(), &engineutil.Opts{})
	require.NoError(t, err)
	// Ordinary initialization does not enable cleanup accounting. Neither a nil
	// store nor a store without an accounted binding may impose its requirement.
	require.NoError(t, srv.removeDaggerSession(t.Context(), sess))
}

type accountingFailedMetricReader struct{ *sdkmetric.ManualReader }

func (accountingFailedMetricReader) Shutdown(context.Context) error {
	return errors.New("original metric shutdown failed")
}

func TestSessionAccountingRetainsEarlierMetricShutdownFailure(t *testing.T) {
	for _, path := range []string{"shutdown-endpoint", "quiescent-reclamation"} {
		t.Run(path, func(t *testing.T) {
			srv, store, dir, md := nativeAccountingFixture(t)
			binding := nativeAuthorizeAndBind(t, srv, md)
			sess := accountingTeardownSession(t, srv, md, binding, false)
			record := sess.clientRecords[md.ClientID]
			client := &clientRuntime{clientRecord: record, state: clientStateInitialized,
				lifecycleLeases: make(map[uint64]clientLifecycleLeaseRecord)}
			record.accepting = true
			record.shutdownCh = make(chan struct{})
			sess.clientRuntimes[md.ClientID] = client
			client.meterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(
				accountingFailedMetricReader{sdkmetric.NewManualReader()}))
			if path == "shutdown-endpoint" {
				// Hold the accepted request lease so /shutdown itself owns this drain;
				// releasing it then uses the ordinary quiescent reclamation path.
				sess.scopeMu.Lock()
				lease := sess.newClientLifecycleLeaseLocked(client, engine.ClientLeaseRequest, "POST /shutdown")
				sess.scopeMu.Unlock()
				req := httptest.NewRequest(http.MethodPost, engine.ShutdownEndpoint, nil)
				err := srv.serveShutdown(httptest.NewRecorder(), req, client)
				require.ErrorContains(t, err, "original metric shutdown failed")
				require.Nil(t, client.meterProvider)
				require.NoError(t, client.shutdownMetrics(t.Context()), "later provider disposal is a no-op")
				lease.Release()
			} else {
				// The real scope owner runs shutdownMetrics, unpublishes the runtime and
				// retains only clientRecord. Its logged failure must survive finalization.
				sess.closeClientScope(client)
			}
			require.NotContains(t, sess.clientRuntimes, md.ClientID)
			require.Same(t, record, sess.clientRecords[md.ClientID])
			sess.lifecycleMu.Lock()
			require.NoError(t, srv.removeDaggerSession(t.Context(), sess))
			sess.lifecycleMu.Unlock()
			srv.retireSession(sess)
			response, status := nativeAccountingRequest(t, srv, md, "status", binding, nil)
			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, "cleanup-uncertain", status.State, "earlier shutdown failure cannot become durable release")
			require.Nil(t, status.Receipt)
			require.Contains(t, status.CleanupError, "original metric shutdown failed")
			require.NoError(t, store.Close())
			recovered, err := OpenSessionAccountingStore(dir, store.limits)
			require.NoError(t, err)
			defer recovered.Close()
			srv.sessionAccounting = recovered
			response, replay := nativeAccountingRequest(t, srv, md, "status", binding, nil)
			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, status, replay)
		})
	}
}

// The native Cloud owner releases asynchronously and logs exporter failures.
// Neither finish nor done attests an accounted transfer or successful cleanup.
// These exporters stay in-process and never acquire Cloud credentials/network.
type accountingCloudShutdownFixture struct {
	forwardTestExporter
	entered chan struct{}
	release chan struct{}
	fail    bool
}

func (e *accountingCloudShutdownFixture) Shutdown(context.Context) error {
	e.shutdowns.Add(1)
	close(e.entered)
	// This in-process gate has one test owner, whose cleanup always unblocks
	// it. A clock deadline must not substitute for the explicit release.
	<-e.release
	if e.fail {
		return errors.New("fixture Cloud exporter shutdown failed")
	}
	return nil
}

func TestSessionAccountingRetainedCloudCannotMintRelease(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "successful-exporter-shutdown", true: "failed-exporter-shutdown"}[failed], func(t *testing.T) {
			srv, store, dir, md := nativeAccountingFixture(t)
			binding := nativeAuthorizeAndBind(t, srv, md)
			sess := accountingTeardownSession(t, srv, md, binding, false)
			exporter := &accountingCloudShutdownFixture{entered: make(chan struct{}), release: make(chan struct{}), fail: failed}
			var release sync.Once
			unblock := func() { release.Do(func() { close(exporter.release) }) }
			tuning := fastCloudForwardTuning()
			srv.cloudForwardTuning = &tuning
			forwarder, err := srv.startCloudForwarder(sess.sessionID, md.ClientID, exporter, &forwardTestExporter{}, tuning.shutdownTimeout)
			require.NoError(t, err, "cloud-control-fixture-failure: acquire forwarder")
			sess.cloudForwarder = forwarder
			t.Cleanup(func() {
				unblock()
				forwarder.stop(errors.New("fixture cleanup"))
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				require.NoError(t, forwarder.waitReleased(ctx))
			})
			sess.lifecycleMu.Lock()
			err = srv.removeDaggerSession(t.Context(), sess)
			sess.lifecycleMu.Unlock()
			require.NoError(t, err)
			srv.retireSession(sess)
			select {
			case <-exporter.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("existing Cloud release owner did not reach exporter shutdown")
			}
			select {
			case <-forwarder.done:
				t.Fatal("fixture did not retain Cloud cleanup after session removal")
			default:
			}
			require.True(t, forwarder.isFinished())
			response, status := nativeAccountingRequest(t, srv, md, "status", binding, nil)
			require.Equal(t, http.StatusOK, response.Code, "cloud-control-fixture-failure: status unavailable")
			require.Equal(t, "cleanup-uncertain", status.State,
				"[cloud-release-state-mismatch test=%s want=cleanup-uncertain got=%s]", t.Name(), status.State)
			require.Nil(t, status.Receipt)
			require.Empty(t, status.Cleanup.ExecHelpersPermits)
			require.Contains(t, status.CleanupError, "retained Cloud forwarding")
			unblock()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			require.NoError(t, forwarder.waitReleased(ctx), "waitReleased reports owner exit even when exporter shutdown fails")
			require.EqualValues(t, 1, exporter.shutdowns.Load())
			response, after := nativeAccountingRequest(t, srv, md, "status", binding, nil)
			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, status, after, "forwarder exit cannot retroactively mint a durable release")
			response, _ = nativeAccountingRequest(t, srv, md, "ack", binding, &NativeSessionReleaseReceipt{ReceiptID: "cloud-finished", NativeSequence: status.Sequence})
			require.Equal(t, http.StatusConflict, response.Code)
			require.NoError(t, store.Close())
			recovered, err := OpenSessionAccountingStore(dir, store.limits)
			require.NoError(t, err)
			defer recovered.Close()
			srv.sessionAccounting = recovered
			response, after = nativeAccountingRequest(t, srv, md, "status", binding, nil)
			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, status, after)
		})
	}
}

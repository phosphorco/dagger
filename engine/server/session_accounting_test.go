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

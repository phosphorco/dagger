package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	bolt "go.etcd.io/bbolt"
)

const sessionAccountingPrefix = "/v1/session-accounting/"

var errSessionAccountingBusy = errors.New("native session lifecycle owner is busy")

// SessionAccountingLimits must be commissioned by the native lifecycle owner.
// These are retention bounds, not resource admission policy or defaults.
type SessionAccountingLimits struct {
	Records       int
	RecordBytes   int
	LogicalBytes  int
	PhysicalBytes int
}

// SessionAccountingStore retains native-owned authorization and cleanup facts,
// never a second session controller. The host must close it after Server stops.
// No runtime/factory enables it by default. Credential verifiers stay root-private
// in this native store and never appear in returned facts, traces or the gateway.
type SessionAccountingStore struct {
	db        *bolt.DB
	limits    SessionAccountingLimits
	fault     atomic.Bool
	path      string
	directory os.FileInfo
	file      os.FileInfo
}

type SessionAccountingBinding struct {
	EngineEpoch      string `json:"engineEpoch"`
	SessionID        string `json:"sessionID"`
	RootClientID     string `json:"rootClientID"`
	Generation       uint64 `json:"generation"`
	HoldID           string `json:"holdID"`
	DispatchSequence uint64 `json:"dispatchSequence"`
	DescriptorRef    string `json:"descriptorRef"`
}

type SessionAccountingCleanup struct {
	Requests           string `json:"requests"`
	ClientScopes       string `json:"clientScopes"`
	Services           string `json:"services"`
	Agents             string `json:"agents"`
	DetachedCache      string `json:"detachedCache"`
	ExecHelpersPermits string `json:"execHelpersPermits"`
}

func (c SessionAccountingCleanup) complete() bool {
	return c.Requests != "" && c.ClientScopes != "" && c.Services != "" && c.Agents != "" && c.DetachedCache != "" && c.ExecHelpersPermits != ""
}

type NativeSessionReleaseReceipt struct {
	Binding        SessionAccountingBinding `json:"binding"`
	ReceiptID      string                   `json:"receiptID"`
	NativeSequence uint64                   `json:"nativeSequence"`
	Cleanup        SessionAccountingCleanup `json:"cleanup"`
}

type SessionAccountingSnapshot struct {
	Binding      SessionAccountingBinding     `json:"binding"`
	Sequence     uint64                       `json:"sequence"`
	Complete     bool                         `json:"complete"` // exact requested binding only, never a list census
	State        string                       `json:"state"`
	Cleanup      SessionAccountingCleanup     `json:"cleanup"`
	CleanupError string                       `json:"cleanupError,omitempty"`
	Receipt      *NativeSessionReleaseReceipt `json:"receipt,omitempty"`
	Acked        bool                         `json:"acked"`
}

type sessionAccountingFact struct {
	Snapshot     SessionAccountingSnapshot `json:"snapshot"`
	RootVerifier [32]byte                  `json:"rootVerifier"` // native credential authority; NEVER in responses
}

func accountingText(values ...string) bool {
	for _, v := range values {
		if v == "" || len(v) > 256 || strings.ContainsAny(v, "\x00\r\n") {
			return false
		}
	}
	return true
}
func accountingKey(epoch, session string) []byte { return []byte(epoch + "\x00" + session) }

// CreateSessionAccountingStore is explicit stopped-source preparation only. It
// requires a fresh private directory; Open never initializes missing state.
func CreateSessionAccountingStore(dir string, limits SessionAccountingLimits) (*SessionAccountingStore, error) {
	return openSessionAccountingStore(dir, limits, true)
}
func OpenSessionAccountingStore(dir string, limits SessionAccountingLimits) (*SessionAccountingStore, error) {
	return openSessionAccountingStore(dir, limits, false)
}
func openSessionAccountingStore(dir string, l SessionAccountingLimits, create bool) (*SessionAccountingStore, error) {
	if l.Records <= 0 || l.RecordBytes <= 0 || l.LogicalBytes <= 0 || l.Records > l.LogicalBytes/l.RecordBytes || l.PhysicalBytes < l.LogicalBytes || l.PhysicalBytes < 4*os.Getpagesize() {
		return nil, errors.New("explicit finite native accounting bounds required")
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0700 || !ok || owner.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("native accounting requires existing owner-private directory")
	}
	if create {
		names, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		if len(names) != 0 {
			return nil, errors.New("native accounting creation refuses retained inputs")
		}
	}
	path := filepath.Join(dir, "session-accounting.db")
	retained, err := os.Lstat(path)
	if create && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("native accounting database already exists")
	}
	if !create {
		if err != nil || !retained.Mode().IsRegular() || retained.Mode().Perm() != 0600 || retained.Size() < int64(2*os.Getpagesize()) || retained.Size() > int64(l.PhysicalBytes) {
			return nil, errors.New("native accounting database missing or permissions changed")
		}
		owner, ok := retained.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
			return nil, errors.New("foreign native accounting database ownership/links")
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	anchored, err := root.Stat(".")
	if err != nil || !os.SameFile(info, anchored) {
		return nil, errors.New("native accounting directory changed")
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Millisecond, NoSync: false, NoGrowSync: false, MaxSize: l.PhysicalBytes,
		OpenFile: func(_ string, flags int, _ os.FileMode) (*os.File, error) {
			flags |= syscall.O_NOFOLLOW
			if create {
				flags |= os.O_CREATE | os.O_EXCL
			} else {
				flags &^= os.O_CREATE
			}
			f, err := root.OpenFile("session-accounting.db", flags, 0600)
			if err != nil {
				return nil, err
			}
			if create {
				err = f.Chmod(0600)
			}
			if err == nil && !create {
				var actual os.FileInfo
				actual, err = f.Stat()
				if err == nil && !os.SameFile(retained, actual) {
					err = errors.New("native accounting database identity changed")
				}
			}
			if err != nil {
				f.Close()
				return nil, err
			}
			return f, nil
		},
	})
	if err != nil {
		return nil, err
	}
	dbInfo, statErr := os.Lstat(path)
	if statErr != nil {
		db.Close()
		return nil, statErr
	}
	store := &SessionAccountingStore{db: db, limits: l, path: path, directory: info, file: dbInfo}
	if create {
		err = store.update(func(tx *bolt.Tx) error {
			_, err := tx.CreateBucket([]byte("native-session-accounting-v1"))
			return err
		})
		if err == nil {
			var parent *os.File
			parent, err = os.Open(dir)
			if err == nil {
				err = parent.Sync()
				_ = parent.Close()
			}
		}
	} else {
		err = db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte("native-session-accounting-v1"))
			if b == nil {
				return errors.New("native accounting schema unavailable")
			}
			count, total := 0, 0
			return b.ForEach(func(k, v []byte) error {
				count++
				total += len(k) + len(v)
				var f sessionAccountingFact
				if count > l.Records || total > l.LogicalBytes || len(v) > l.RecordBytes || json.Unmarshal(v, &f) != nil || f.Snapshot.Binding.Generation == 0 || f.Snapshot.Sequence == 0 || string(k) != string(accountingKey(f.Snapshot.Binding.EngineEpoch, f.Snapshot.Binding.SessionID)) {
					return errors.New("retained native accounting facts incompatible")
				}
				if f.Snapshot.Receipt != nil && (f.Snapshot.State != "released" || !f.Snapshot.Cleanup.complete() || f.Snapshot.Receipt.Binding != f.Snapshot.Binding || f.Snapshot.Receipt.NativeSequence != f.Snapshot.Sequence) {
					return errors.New("incompatible native cleanup receipt")
				}
				return nil
			})
		})
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}
func (s *SessionAccountingStore) Close() error { return s.db.Close() }
func (s *SessionAccountingStore) checkStorage() error {
	if s.fault.Load() {
		return errors.New("native accounting storage is uncertain")
	}
	dir, dirErr := os.Lstat(filepath.Dir(s.path))
	file, fileErr := os.Lstat(s.path)
	if dirErr != nil || fileErr != nil || !os.SameFile(s.directory, dir) || !os.SameFile(s.file, file) || dir.Mode().Perm() != 0700 || file.Mode().Perm() != 0600 {
		s.fault.Store(true)
		return errors.New("native accounting storage identity unavailable")
	}
	owner, ok := file.Sys().(*syscall.Stat_t)
	dirOwner, dirOK := dir.Sys().(*syscall.Stat_t)
	if !ok || !dirOK || owner.Uid != uint32(os.Geteuid()) || dirOwner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
		s.fault.Store(true)
		return errors.New("native accounting storage ownership changed")
	}
	return nil
}
func (s *SessionAccountingStore) update(fn func(*bolt.Tx) error) error {
	if err := s.checkStorage(); err != nil {
		return err
	}
	var rejected error
	err := s.db.Update(func(tx *bolt.Tx) error { rejected = fn(tx); return rejected })
	if err != nil && rejected == nil {
		s.fault.Store(true)
	}
	if err == nil {
		err = s.checkStorage()
	}
	return err
}
func (s *SessionAccountingStore) read(epoch, session string) (sessionAccountingFact, error) {
	var fact sessionAccountingFact
	if err := s.checkStorage(); err != nil {
		return fact, err
	}
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte("native-session-accounting-v1")).Get(accountingKey(epoch, session))
		if raw == nil {
			return os.ErrNotExist
		}
		if len(raw) > s.limits.RecordBytes {
			return errors.New("native accounting fact exceeds retained bound")
		}
		return json.Unmarshal(raw, &fact)
	})
	return fact, err
}
func (s *SessionAccountingStore) put(tx *bolt.Tx, f sessionAccountingFact) error {
	bucket := tx.Bucket([]byte("native-session-accounting-v1"))
	key := accountingKey(f.Snapshot.Binding.EngineEpoch, f.Snapshot.Binding.SessionID)
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if len(raw) > s.limits.RecordBytes {
		return errors.New("native accounting record bound reached")
	}
	count, total := 0, 0
	if err = bucket.ForEach(func(k, v []byte) error { count++; total += len(k) + len(v); return nil }); err != nil {
		return err
	}
	previous := bucket.Get(key)
	if previous == nil && count >= s.limits.Records {
		return errors.New("native accounting retention full; retained outcomes cannot expire")
	}
	if previous != nil {
		total -= len(key) + len(previous)
	}
	if total+len(key)+len(raw) > s.limits.LogicalBytes {
		return errors.New("native accounting byte bound reached")
	}
	return bucket.Put(key, raw)
}
func (s *SessionAccountingStore) authorize(epoch string, md *engine.ClientMetadata, descriptor string) (sessionAccountingFact, error) {
	var f sessionAccountingFact
	if !accountingText(epoch, md.SessionID, md.ClientID, descriptor) || md.ClientSecretToken == "" || len(md.ClientSecretToken) > 1024 {
		return f, errors.New("bounded native root credentials required")
	}
	err := s.update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("native-session-accounting-v1"))
		raw := b.Get(accountingKey(epoch, md.SessionID))
		if raw != nil {
			if err := json.Unmarshal(raw, &f); err != nil {
				return err
			}
			if !f.authenticates(md) || f.Snapshot.Binding.DescriptorRef != descriptor {
				return errors.New("native root/descriptor conflict")
			}
			if f.Snapshot.State == "released" || f.Snapshot.State == "cleanup-uncertain" {
				return errors.New("native accounting root cannot be resurrected")
			}
			return nil
		}
		seq, err := b.NextSequence()
		if err != nil {
			return err
		}
		f = sessionAccountingFact{RootVerifier: sha256.Sum256([]byte(md.ClientSecretToken)), Snapshot: SessionAccountingSnapshot{Binding: SessionAccountingBinding{EngineEpoch: epoch, SessionID: md.SessionID, RootClientID: md.ClientID, Generation: seq, DescriptorRef: descriptor}, Sequence: seq, Complete: true, State: "authorized"}}
		return s.put(tx, f)
	})
	return f, err
}
func (f sessionAccountingFact) authenticates(md *engine.ClientMetadata) bool {
	got := sha256.Sum256([]byte(md.ClientSecretToken))
	return md.ClientSecretToken != "" && f.Snapshot.Binding.SessionID == md.SessionID && f.Snapshot.Binding.RootClientID == md.ClientID && subtle.ConstantTimeCompare(f.RootVerifier[:], got[:]) == 1
}
func (s *SessionAccountingStore) bind(md *engine.ClientMetadata, expected SessionAccountingBinding) (SessionAccountingSnapshot, error) {
	var result SessionAccountingSnapshot
	err := s.update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("native-session-accounting-v1"))
		raw := b.Get(accountingKey(expected.EngineEpoch, md.SessionID))
		if raw == nil {
			return os.ErrNotExist
		}
		var f sessionAccountingFact
		if err := json.Unmarshal(raw, &f); err != nil {
			return err
		}
		old := f.Snapshot.Binding
		if !f.authenticates(md) || old.Generation != expected.Generation || old.RootClientID != expected.RootClientID || old.SessionID != expected.SessionID || old.DescriptorRef != expected.DescriptorRef || !accountingText(expected.HoldID) || expected.DispatchSequence == 0 {
			return errors.New("native dispatch binding conflict")
		}
		if old.HoldID != "" {
			if old != expected {
				return errors.New("native original binding is immutable")
			}
			result = f.Snapshot
			return nil
		}
		if f.Snapshot.State != "authorized" {
			return errors.New("native authorization is not bindable")
		}
		seq, err := b.NextSequence()
		if err != nil {
			return err
		}
		f.Snapshot.Binding = expected
		f.Snapshot.Sequence = seq
		f.Snapshot.State = "bound"
		if err = s.put(tx, f); err != nil {
			return err
		}
		result = f.Snapshot
		return nil
	})
	return result, err
}
func (s *SessionAccountingStore) finish(binding SessionAccountingBinding, cleanup SessionAccountingCleanup, cleanupErr error) error {
	return s.update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("native-session-accounting-v1"))
		raw := bucket.Get(accountingKey(binding.EngineEpoch, binding.SessionID))
		if raw == nil {
			return os.ErrNotExist
		}
		var f sessionAccountingFact
		if err := json.Unmarshal(raw, &f); err != nil {
			return err
		}
		if f.Snapshot.Binding != binding {
			return errors.New("native teardown original binding conflict")
		}
		// Terminal facts are immutable. Cleanup uncertainty cannot be retried into
		// success after the resource owner has forgotten its original errors.
		if f.Snapshot.State == "released" || f.Snapshot.State == "cleanup-uncertain" {
			return nil
		}
		seq, err := bucket.NextSequence()
		if err != nil {
			return err
		}
		f.Snapshot.Sequence = seq
		f.Snapshot.Cleanup = cleanup
		f.Snapshot.State = "cleanup-uncertain"
		if cleanupErr != nil {
			f.Snapshot.CleanupError = cleanupErr.Error()
		}
		if cleanupErr == nil && cleanup.complete() && binding.DispatchSequence > 0 {
			f.Snapshot.State = "released"
			f.Snapshot.Receipt = &NativeSessionReleaseReceipt{Binding: binding, ReceiptID: fmt.Sprintf("%s/%d/%d", binding.EngineEpoch, binding.Generation, seq), NativeSequence: seq, Cleanup: cleanup}
		}
		return s.put(tx, f)
	})
}
func (s *SessionAccountingStore) ack(md *engine.ClientMetadata, expected SessionAccountingBinding, receipt string, sequence uint64) error {
	return s.update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("native-session-accounting-v1"))
		raw := b.Get(accountingKey(expected.EngineEpoch, md.SessionID))
		if raw == nil {
			return os.ErrNotExist
		}
		var f sessionAccountingFact
		if err := json.Unmarshal(raw, &f); err != nil {
			return err
		}
		r := f.Snapshot.Receipt
		if !f.authenticates(md) || f.Snapshot.Binding != expected || r == nil || receipt != r.ReceiptID || sequence != r.NativeSequence {
			return errors.New("native acknowledgement conflict")
		}
		if f.Snapshot.Acked {
			return nil
		}
		f.Snapshot.Acked = true
		// Keep credential authority and the exact ack tombstone; no age-based GC.
		return s.put(tx, f)
	})
}

// sessionAccountingInitError marks only a rejection from the new accounting
// check before initialization. It preserves the error chain and does not change
// authentication, lifecycle ownership or legacy request failures.
type sessionAccountingInitError struct{ error }

func (e sessionAccountingInitError) Unwrap() error { return e.error }

// Accounting controls and accounting rejections before initialization cannot
// hijack the connection. Reuse the native error writer before any response write;
// the legacy handler's hijack probe would otherwise commit HTTP 200 first.
func writeSessionAccountingHTTPError(w http.ResponseWriter, r *http.Request, err error) {
	bklog.G(r.Context()).
		WithField("method", r.Method).
		WithField("path", r.URL.Path).
		WithError(err).Error("failed to serve request")
	var response httpError
	if !errors.As(err, &response) {
		response = httpErr(err, http.StatusInternalServerError)
	}
	response.WriteTo(w)
}

// New control operations share the existing native HTTP/auth boundary. They
// never initialize runtime/telemetry/services, execute work or mint credentials.
func (srv *Server) serveSessionAccountingHTTP(w http.ResponseWriter, r *http.Request, opts *ClientInitOpts) error {
	if srv.sessionAccounting == nil {
		return httpErr(errors.New("native session accounting is disabled"), http.StatusNotFound)
	}
	if r.Method != http.MethodPost {
		return httpErr(errors.New("method not allowed"), http.StatusMethodNotAllowed)
	}
	if opts == nil || opts.ClientMetadata == nil {
		return httpErr(errors.New("native client identity required"), http.StatusUnauthorized)
	}
	md := opts.ClientMetadata
	var input struct {
		Binding        SessionAccountingBinding `json:"binding"`
		ReceiptID      string                   `json:"receiptID"`
		NativeSequence uint64                   `json:"nativeSequence"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return httpErr(errors.New("invalid bounded accounting request"), http.StatusBadRequest)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return httpErr(errors.New("one accounting request required"), http.StatusBadRequest)
	}
	if err := r.Context().Err(); err != nil {
		return err
	}
	op := strings.TrimPrefix(r.URL.Path, sessionAccountingPrefix)
	var result SessionAccountingSnapshot
	switch op {
	case "authorize":
		if input.Binding.EngineEpoch != srv.engineInstanceID || input.Binding.SessionID != md.SessionID || input.Binding.RootClientID != md.ClientID {
			return httpErr(errors.New("native root binding mismatch"), http.StatusUnauthorized)
		}
		f, err := srv.authorizeSessionAccountingRoot(md, input.Binding.DescriptorRef)
		if err != nil {
			if errors.Is(err, errSessionAccountingBusy) {
				return httpErr(err, http.StatusServiceUnavailable)
			}
			return httpErr(err, http.StatusConflict)
		}
		result = f.Snapshot
	case "bind":
		if input.Binding.EngineEpoch != srv.engineInstanceID {
			return httpErr(errors.New("native current epoch required for binding"), http.StatusConflict)
		}
		var err error
		result, err = srv.sessionAccounting.bind(md, input.Binding)
		if err != nil {
			return httpErr(err, http.StatusConflict)
		}
	case "ancestry":
		verdict, err := srv.sessionAccountingAncestry(md, input.Binding)
		if err != nil {
			if errors.Is(err, errSessionAccountingBusy) {
				return httpErr(err, http.StatusServiceUnavailable)
			}
			return httpErr(err, http.StatusUnauthorized)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		return json.NewEncoder(w).Encode(verdict)
	case "status", "ack":
		f, err := srv.sessionAccounting.read(input.Binding.EngineEpoch, md.SessionID)
		if err != nil {
			return httpErr(errors.New("native accounting exact scope unavailable"), http.StatusServiceUnavailable)
		}
		if !f.authenticates(md) || f.Snapshot.Binding != input.Binding {
			return httpErr(errors.New("native original root credentials required"), http.StatusUnauthorized)
		}
		if op == "ack" {
			if err = srv.sessionAccounting.ack(md, input.Binding, input.ReceiptID, input.NativeSequence); err != nil {
				return httpErr(err, http.StatusConflict)
			}
			f.Snapshot.Acked = true
		}
		result = f.Snapshot
		if result.State != "released" && result.State != "cleanup-uncertain" {
			// Registry absence is UNKNOWN. An old engine process's nonterminal fact
			// remains unknown even if the new process reuses the same session string.
			result.State = "unknown"
			if input.Binding.EngineEpoch == srv.engineInstanceID {
				srv.daggerSessionsMu.RLock()
				sess := srv.daggerSessions[md.SessionID]
				srv.daggerSessionsMu.RUnlock()
				if sess != nil {
					// Teardown may hold lifecycleMu through an unbounded producer drain.
					// An observation never waits on that owner or guesses absence is cleanup.
					if sess.lifecycleMu.TryLock() {
						if sess.accountingBinding == input.Binding {
							if f.Snapshot.State == "initialized" && sess.state.Load() == sessionStateInitialized {
								result.State = "running"
							}
							if f.Snapshot.State == "closing" {
								result.State = "closing"
							}
						}
						sess.lifecycleMu.Unlock()
					} else if f.Snapshot.State == "closing" {
						// This exact generation/intent closing fact was durably published by
						// the lifecycle owner before teardown; it is not a release receipt.
						result.State = "closing"
					} else {
						result.Complete = false
					}
				}
			}
		}
	default:
		return httpErr(errors.New("unknown accounting operation"), http.StatusNotFound)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	return json.NewEncoder(w).Encode(result)
}

// This extends the existing native root/client owner, using its ordinary
// uninitialized session and client records, not a shadow lifecycle registry.
func (srv *Server) authorizeSessionAccountingRoot(md *engine.ClientMetadata, descriptor string) (sessionAccountingFact, error) {
	var empty sessionAccountingFact
	if srv.isShuttingDown() {
		return empty, errors.New("engine is closing")
	}
	if !accountingText(md.SessionID, md.ClientID, descriptor) || md.ClientSecretToken == "" || len(md.ClientSecretToken) > 1024 {
		return empty, errors.New("bounded native root credentials required")
	}
	srv.daggerSessionsMu.Lock()
	sess, created, err := srv.getOrCreateSessionLocked(md.SessionID, md.ClientID)
	srv.daggerSessionsMu.Unlock()
	if err != nil {
		return empty, err
	}
	if !created && !sess.lifecycleMu.TryLock() {
		return empty, errSessionAccountingBusy
	}
	successful := false
	defer func() {
		sess.lifecycleMu.Unlock()
		if created && !successful {
			srv.deleteSession(sess)
		}
	}()
	if sess.state.Load() == sessionStateInitialized && sess.accountingBinding.Generation == 0 {
		return empty, errors.New("existing unaccounted session cannot be enrolled")
	}
	if sess.state.Load() == sessionStateRemoved || sess.mainClientCallerID != md.ClientID {
		return empty, errors.New("native original root required")
	}
	sess.scopeMu.Lock()
	defer sess.scopeMu.Unlock()
	sess.clientMu.RLock()
	record := sess.clientRecords[md.ClientID]
	sess.clientMu.RUnlock()
	if record != nil {
		if record.clientMetadata == nil || !record.accepting || subtle.ConstantTimeCompare([]byte(record.clientMetadata.ClientSecretToken), []byte(md.ClientSecretToken)) != 1 {
			return empty, errors.New("native root credential mismatch")
		}
	}
	f, err := srv.sessionAccounting.authorize(srv.engineInstanceID, md, descriptor)
	if err != nil {
		return empty, err
	}
	if record == nil {
		// Minimal bootstrap metadata is completed/sealed by existing getOrInitClient.
		record = &clientRecord{daggerSession: sess, clientID: md.ClientID, clientMetadata: &engine.ClientMetadata{SessionID: md.SessionID, ClientID: md.ClientID, ClientSecretToken: md.ClientSecretToken}, shutdownCh: make(chan struct{}), accepting: true}
		runtime := &clientRuntime{clientRecord: record, state: clientStateUninitialized, lifecycleLeases: make(map[uint64]clientLifecycleLeaseRecord)}
		sess.clientMu.Lock()
		sess.clientRecords[md.ClientID] = record
		sess.clientRuntimes[md.ClientID] = runtime
		sess.clientMu.Unlock()
	}
	sess.accountingBinding = f.Snapshot.Binding
	successful = true
	return f, nil
}

func (srv *Server) checkSessionAccountingBeforeInit(sess *daggerSession, md *engine.ClientMetadata, root bool) error {
	if srv.sessionAccounting == nil {
		return nil
	}
	f, err := srv.sessionAccounting.read(srv.engineInstanceID, md.SessionID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} // unaccounted native callers stay outside this guarantee
	if err != nil {
		return err
	}
	if f.Snapshot.Binding.DispatchSequence == 0 || (f.Snapshot.State != "bound" && !(f.Snapshot.State == "initialized" && sess.state.Load() == sessionStateInitialized)) {
		return errors.New("native accounting intent not durably bound")
	}
	if root && (md.ClientID != sess.mainClientCallerID || !f.authenticates(md)) {
		return errors.New("native original root credential mismatch")
	}
	sess.accountingBinding = f.Snapshot.Binding
	return nil
}

func (srv *Server) finishSessionAccounting(sess *daggerSession, cleanup SessionAccountingCleanup, err error) error {
	if srv.sessionAccounting == nil || sess.accountingBinding.Generation == 0 {
		return nil
	}
	return srv.sessionAccounting.finish(sess.accountingBinding, cleanup, err)
}

func (s *SessionAccountingStore) state(binding SessionAccountingBinding, state string) error {
	return s.update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("native-session-accounting-v1"))
		raw := b.Get(accountingKey(binding.EngineEpoch, binding.SessionID))
		if raw == nil {
			return os.ErrNotExist
		}
		var f sessionAccountingFact
		if err := json.Unmarshal(raw, &f); err != nil {
			return err
		}
		if f.Snapshot.Binding != binding {
			return errors.New("native runtime binding conflict")
		}
		if f.Snapshot.State == state {
			return nil
		}
		if (state == "initialized" && f.Snapshot.State != "bound") || (state == "closing" && f.Snapshot.State != "initialized") {
			return errors.New("native accounting transition conflict")
		}
		seq, err := b.NextSequence()
		if err != nil {
			return err
		}
		f.Snapshot.Sequence = seq
		f.Snapshot.State = state
		return s.put(tx, f)
	})
}
func (srv *Server) markSessionAccountingState(sess *daggerSession, state string) error {
	if srv.sessionAccounting == nil || sess.accountingBinding.Generation == 0 {
		return nil
	}
	return srv.sessionAccounting.state(sess.accountingBinding, state)
}

type SessionAccountingClientVerdict struct {
	Binding         SessionAccountingBinding `json:"binding"`
	ClientID        string                   `json:"clientID"`
	ParentClientIDs []string                 `json:"parentClientIDs"`
}

func (srv *Server) sessionAccountingAncestry(md *engine.ClientMetadata, binding SessionAccountingBinding) (SessionAccountingClientVerdict, error) {
	var out SessionAccountingClientVerdict
	srv.daggerSessionsMu.RLock()
	sess := srv.daggerSessions[md.SessionID]
	srv.daggerSessionsMu.RUnlock()
	if sess == nil || binding.EngineEpoch != srv.engineInstanceID {
		return out, errors.New("native registered ancestry unavailable")
	}
	if !sess.lifecycleMu.TryLock() {
		return out, errSessionAccountingBusy
	}
	defer sess.lifecycleMu.Unlock()
	if sess.accountingBinding != binding || sess.state.Load() != sessionStateInitialized {
		return out, errors.New("native original session not live")
	}
	sess.scopeMu.Lock()
	defer sess.scopeMu.Unlock()
	sess.clientMu.RLock()
	record := sess.clientRecords[md.ClientID]
	sess.clientMu.RUnlock()
	if record == nil || !record.accepting || record.clientMetadata == nil || subtle.ConstantTimeCompare([]byte(record.clientMetadata.ClientSecretToken), []byte(md.ClientSecretToken)) != 1 {
		return out, errors.New("native client credential mismatch")
	}
	if md.ClientID != binding.RootClientID {
		if len(record.parentClientIDs) == 0 || record.parentClientIDs[0] != binding.RootClientID || record.nestedTransport == nil || record.nestedTransport.Closed() {
			return out, errors.New("native nested transport ancestry required")
		}
	}
	out = SessionAccountingClientVerdict{Binding: binding, ClientID: md.ClientID, ParentClientIDs: append([]string{}, record.parentClientIDs...)}
	return out, nil
}

// The stable client record outlives quiescent runtime reclamation. A successful
// later no-op shutdown cannot erase an earlier owner's actual cleanup failure.
func (sess *daggerSession) retainedMetricShutdownError() error {
	var errs error
	sess.clientMu.RLock()
	defer sess.clientMu.RUnlock()
	for id, record := range sess.clientRecords {
		if failure := record.metricShutdownFailure.Load(); failure != nil {
			errs = errors.Join(errs, fmt.Errorf("client %s metric shutdown: %s", id, *failure))
		}
	}
	return errs
}

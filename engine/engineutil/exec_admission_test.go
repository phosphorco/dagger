package engineutil

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
)

func admissionClients(t *testing.T, limit int64) (*Client, *Client) {
	t.Helper()
	opts, err := NewOpts(Opts{ParallelismSem: semaphore.NewWeighted(limit)})
	require.NoError(t, err)
	// The server makes these exact shallow copies for independent sessions.
	first, second := *opts, *opts
	return &Client{Opts: &first}, &Client{Opts: &second}
}

func admissionState(id string) *execState {
	// The schema's explicit disableDaggerInDagger=true leaf has no nested
	// client metadata or new-session endpoint. Each id also names a session.
	return &execState{id: id, sessionID: id, cleanups: new(cleanups.Cleanups), done: make(chan struct{}), startedOnce: new(sync.Once)}
}

func TestExecutionAdmissionDeclaredLeavesShareBoundAcrossSessionsAndRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first, second := admissionClients(t, 2)
		entered, release := make(chan string, 3), make(chan struct{})
		var unblock sync.Once
		defer unblock.Do(func() { close(release) })
		var active, peak atomic.Int32
		run := namedSetupFunc{"fake process", func(ctx context.Context, state *execState) error {
			n := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); n > old; old = peak.Load() {
				if peak.CompareAndSwap(old, n) {
					break
				}
			}
			entered <- state.id
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}
		done := make(chan error, 2)
		for i, client := range []*Client{first, second} {
			go func() { done <- client.run(t.Context(), admissionState(fmt.Sprint(i)), run) }()
		}
		for range 2 {
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("admitted work did not start")
			}
		}
		// All clients, contexts, semaphore channels and fake processes are created
		// inside this bubble. Acquire releases its mutex before selecting on its
		// ready/context channels, which are both associated with the bubble.
		liveDone := make(chan error, 1)
		go func() { liveDone <- first.run(t.Context(), admissionState("live queued session"), run) }()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		thirdOpts := *first.Opts
		third := &Client{Opts: &thirdOpts}
		cancelledDone := make(chan error, 1)
		go func() { cancelledDone <- third.run(ctx, admissionState("cancelled queued session"), run) }()
		// Wait cannot return while either request is merely unscheduled. Holders
		// are blocked on release; live/cancellable waiters must reach durable
		// blocking or return. A TryAcquire refusal completes into a buffered
		// result channel, and the checks below reject it before capacity opens.
		synctest.Wait()
		select {
		case id := <-entered:
			t.Fatalf("container started across shared sessions while two permits held: %s", id)
		case err := <-liveDone:
			t.Fatalf("live saturated request returned before release: %v", err)
		case err := <-cancelledDone:
			t.Fatalf("uncancelled saturated request returned before release: %v", err)
		default:
		}
		cancel()
		synctest.Wait()
		select {
		case err := <-cancelledDone:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(time.Second):
			t.Fatal("queued cancellation did not finish")
		}
		select {
		case err := <-liveDone:
			t.Fatalf("cancelling a different waiter completed the live request: %v", err)
		default:
		}
		unblock.Do(func() { close(release) })
		synctest.Wait()
		select {
		case id := <-entered:
			require.Equal(t, "live queued session", id)
		case <-time.After(time.Second):
			t.Fatal("already-pending live request did not resume after release")
		}
		select {
		case err := <-liveDone:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("resumed live request did not finish")
		}
		for range 2 {
			require.NoError(t, <-done)
		}
		require.Equal(t, int32(2), peak.Load())
		require.NoError(t, first.run(t.Context(), admissionState("after release"), namedSetupFunc{"next", func(context.Context, *execState) error { return nil }}))
	})
}

func TestExecutionAdmissionHoldsPermitThroughFailureCleanup(t *testing.T) {
	first, second := admissionClients(t, 1)
	cleanupEntered, releaseCleanup := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(releaseCleanup) })
	state := admissionState("failed")
	cleanupErr := errors.New("cleanup failure")
	state.cleanups.Add("fake resource cleanup", func() error { close(cleanupEntered); <-releaseCleanup; return cleanupErr })
	runErr := errors.New("process failed")
	done := make(chan error, 1)
	go func() {
		done <- first.run(t.Context(), state, namedSetupFunc{"fail", func(context.Context, *execState) error { return runErr }})
	}()
	select {
	case <-cleanupEntered:
	case <-time.After(time.Second):
		t.Fatal("cleanup not reached")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var ran atomic.Bool
	err := second.run(ctx, admissionState("cancelled while cleanup owns permit"), namedSetupFunc{"must not start", func(context.Context, *execState) error { ran.Store(true); return nil }})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, ran.Load())
	require.False(t, second.ParallelismSem.TryAcquire(1), "permit released before resource cleanup ended")
	unblock.Do(func() { close(releaseCleanup) })
	err = <-done
	require.ErrorIs(t, err, runErr)
	require.ErrorIs(t, err, cleanupErr)
	require.True(t, second.ParallelismSem.TryAcquire(1), "failed cleanup leaked admission permit")
	second.ParallelismSem.Release(1)
}

func TestExecutionAdmissionActiveCancellationReleases(t *testing.T) {
	first, second := admissionClients(t, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- first.run(ctx, admissionState("active"), namedSetupFunc{"fake process", func(ctx context.Context, _ *execState) error { close(started); <-ctx.Done(); return ctx.Err() }})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("process not reached")
	}
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.True(t, second.ParallelismSem.TryAcquire(1))
	second.ParallelismSem.Release(1)
}

func TestExecutionAdmissionParentsDoNotHoldDescendantCapacity(t *testing.T) {
	for _, kind := range []string{"service", "SDK invocation", "new session endpoint", "CA installer"} {
		t.Run(kind, func(t *testing.T) {
			client, childClient := admissionClients(t, 1)
			parent := admissionState("parent")
			switch kind {
			case "service", "CA installer":
				parent.startedCh = make(chan struct{})
			case "SDK invocation":
				parent.nestedClientMetadata = &engine.ClientMetadata{ClientID: "nested"}
				parent.execMD = &ExecutionMetadata{Internal: true}
			case "new session endpoint":
				parent.execMD = &ExecutionMetadata{DaggerInDaggerNewSession: true}
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			err := client.run(ctx, parent, namedSetupFunc{"parent awaiting child", func(ctx context.Context, _ *execState) error {
				return childClient.run(ctx, admissionState("leaf child"), namedSetupFunc{"child", func(context.Context, *execState) error { return nil }})
			}})
			require.NoError(t, err)
			require.True(t, client.ParallelismSem.TryAcquire(1))
			client.ParallelismSem.Release(1)
		})
	}
}

func TestExecutionAdmissionMarkersDoNotExemptOrdinaryLeaves(t *testing.T) {
	for _, kind := range []string{"plain", "internal marker alone", "empty nested metadata"} {
		t.Run(kind, func(t *testing.T) {
			client, _ := admissionClients(t, 1)
			require.NoError(t, client.ParallelismSem.Acquire(t.Context(), 1))
			defer client.ParallelismSem.Release(1)
			state := admissionState(kind)
			if kind == "internal marker alone" {
				state.execMD = &ExecutionMetadata{Internal: true}
			}
			if kind == "empty nested metadata" {
				state.nestedClientMetadata = &engine.ClientMetadata{}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			var ran bool
			err := client.run(ctx, state, namedSetupFunc{"must not start", func(context.Context, *execState) error { ran = true; return nil }})
			require.ErrorIs(t, err, context.Canceled)
			require.False(t, ran)
		})
	}
}

func TestExecutionAdmissionNilPreservesUnlimitedDefault(t *testing.T) {
	opts, err := NewOpts(Opts{})
	require.NoError(t, err)
	client := &Client{Opts: opts}
	require.NoError(t, client.run(t.Context(), admissionState("unlimited"), namedSetupFunc{"run", func(context.Context, *execState) error { return nil }}))
}

// Current-v1 commands keep nested API access by default. They are outside the
// admitted no-child domain, even if they happen to perform CPU-only work.
func TestExecutionAdmissionDefaultNestedCommandsRemainOutsideBound(t *testing.T) {
	client, _ := admissionClients(t, 1)
	require.NoError(t, client.ParallelismSem.Acquire(t.Context(), 1))
	defer client.ParallelismSem.Release(1)
	state := admissionState("outside-domain default-nested command")
	state.nestedClientMetadata = &engine.ClientMetadata{ClientID: "default-socket"}
	var ran bool
	err := client.run(t.Context(), state, namedSetupFunc{"outside bound", func(context.Context, *execState) error { ran = true; return nil }})
	require.NoError(t, err)
	require.True(t, ran, "the outside-domain command must not consume a leaf slot")
	require.False(t, client.ParallelismSem.TryAcquire(1), "outside command released another run's permit")
}

// Opted-out leaves are counted and cancelled before any setup/process begins.
func TestExecutionAdmissionDeclaredNoChildLeafWaitsForSlot(t *testing.T) {
	client, _ := admissionClients(t, 1)
	require.NoError(t, client.ParallelismSem.Acquire(t.Context(), 1))
	defer client.ParallelismSem.Release(1)
	state := admissionState("declared no-child leaf")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var ran bool
	err := client.run(ctx, state, namedSetupFunc{"must not start", func(context.Context, *execState) error { ran = true; return nil }})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, ran)
}

// Outside-domain default-nested parents can await admitted no-child children;
// no parent holds the slot those children need.
func TestExecutionAdmissionDefaultNestedUserCanAwaitCountedChild(t *testing.T) {
	parentClient, childClient := admissionClients(t, 1)
	parent := admissionState("ordinary nested user command")
	parent.nestedClientMetadata = &engine.ClientMetadata{ClientID: "default-socket"}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err := parentClient.run(ctx, parent, namedSetupFunc{"nested user awaiting child", func(ctx context.Context, _ *execState) error {
		return childClient.run(ctx, admissionState("nested user leaf child"), namedSetupFunc{"child", func(context.Context, *execState) error { return nil }})
	}})
	require.NoError(t, err)
}

func TestExecutionAdmissionCAHelperDoesNotReacquireOuterSlot(t *testing.T) {
	client, _ := admissionClients(t, 1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err := client.run(ctx, admissionState("outer ordinary exec"), namedSetupFunc{"CA installation", func(ctx context.Context, _ *execState) error {
		helper := admissionState("CA helper")
		helper.startedCh = make(chan struct{})
		return client.run(ctx, helper, namedSetupFunc{"installer process", func(context.Context, *execState) error { return nil }})
	}})
	require.NoError(t, err)
	require.True(t, client.ParallelismSem.TryAcquire(1))
	client.ParallelismSem.Release(1)
}

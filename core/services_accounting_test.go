package core

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

type accountingFailedServiceRef struct {
	bkcache.Ref
	calls atomic.Int32
	cause error
}

func (r *accountingFailedServiceRef) Release(context.Context) error { r.calls.Add(1); return r.cause }

type accountingServiceOwnerFixture struct {
	owner               *Services
	exited              chan struct{}
	ref                 *accountingFailedServiceRef
	localErr            error
	failStart           bool
	observedBeforeStart bool
}

func (f *accountingServiceOwnerFixture) Start(_ context.Context, r *RunningService, _ digest.Digest, _ ServiceStartOpts) error {
	f.observedBeforeStart = f.owner.HasObservedStarts()
	// This is the actual tracked-ref owner list consumed by releaseAfterExit.
	if f.ref != nil {
		r.refs = []bkcache.Ref{f.ref}
	}
	if f.failStart {
		return f.localErr
	}
	_, cleanupErr := classifyContainerServiceExit(nil, f.localErr, trace.SpanContext{})
	r.Stop = func(context.Context, bool) error { return cleanupErr }
	r.Wait = func(ctx context.Context) error {
		select {
		case <-f.exited:
			return f.localErr
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	return nil
}
func TestServicesAccountingObservationSurvivesRetirement(t *testing.T) {
	for _, mode := range []string{"success", "local-cleanup-error", "ref-cleanup-error", "failed-start"} {
		t.Run(mode, func(t *testing.T) {
			owner := NewServices()
			require.False(t, owner.HasObservedStarts())
			f := &accountingServiceOwnerFixture{owner: owner, exited: make(chan struct{}), failStart: mode == "failed-start"}
			if mode == "local-cleanup-error" || f.failStart {
				f.localErr = errors.New("active/output refs release failed")
			}
			if mode == "ref-cleanup-error" {
				f.ref = &accountingFailedServiceRef{cause: errors.New("tracked ref release failed")}
			}
			ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{SessionID: "service-owner", ClientID: "root"})
			running, err := owner.Start(ctx, digest.FromString(mode), f, false)
			if f.failStart {
				require.ErrorIs(t, err, f.localErr)
			} else {
				require.NoError(t, err)
				close(f.exited)
				require.Eventually(t, func() bool { return len(owner.ExitedServices("service-owner")) == 1 }, time.Second, time.Millisecond)
				if f.ref != nil {
					require.Eventually(t, func() bool { return f.ref.calls.Load() == 1 }, time.Second, time.Millisecond, "real retirement must run tracked-ref cleanup")
					// Once.Do's local result is gone on a second call: this return alone is
					// intentionally NOT upgraded into positive native cleanup authority.
					require.NoError(t, running.releaseAfterExit(ctx))
				}
			}
			require.True(t, f.observedBeforeStart, "observation must precede any Startable")
			require.NoError(t, owner.StopSessionServices(ctx, "service-owner"))
			require.Empty(t, owner.RunningServices("service-owner"))
			require.Empty(t, owner.ExitedServices("service-owner"))
			require.True(t, owner.HasObservedStarts(), "retirement/failure/listing removal must not erase observed service ownership")
		})
	}
}

package dangshared

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

type concurrentNestedServer struct {
	nestedTransportTestServer
	active   atomic.Int32
	peak     atomic.Int32
	arrivals chan struct{}
	release  chan struct{}
}

func (srv *concurrentNestedServer) ServeHTTPToNestedClient(
	w http.ResponseWriter, _ *http.Request, _ *engine.NestedClientTransport,
	_ *engine.ClientMetadata, _ string, _ bool,
	_ dagql.AnyObjectResult, _ dagql.Typed,
) {
	active := srv.active.Add(1)
	defer srv.active.Add(-1)
	for {
		peak := srv.peak.Load()
		if active <= peak || srv.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	srv.served.Add(1)
	srv.arrivals <- struct{}{}
	<-srv.release
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"data":{}}`))
}

func TestNestedClientBoundsActiveRequestsAndCompletesQueuedWork(t *testing.T) {
	t.Parallel()
	const requests = 256
	srv := &concurrentNestedServer{arrivals: make(chan struct{}, requests), release: make(chan struct{})}
	var release sync.Once
	unblock := func() { release.Do(func() { close(srv.release) }) }
	t.Cleanup(unblock)

	_, err := WithNestedClientServer(
		scopedContext(t, "owner"), &core.Query{Server: srv},
		&engine.ClientMetadata{SessionID: "session", ClientID: "child"},
		false, nil, dagql.ObjectResult[*core.Module]{},
		func(ctx context.Context, client graphql.Client) ([]byte, error) {
			defer unblock()
			var work errgroup.Group
			for range requests {
				work.Go(func() error {
					return client.MakeRequest(ctx, &graphql.Request{Query: "{}"}, &graphql.Response{Data: &struct{}{}})
				})
			}
			for range 64 {
				select {
				case <-srv.arrivals:
				case <-time.After(5 * time.Second):
					t.Fatal("the pool must allow concurrent progress")
				}
			}
			require.Never(t, func() bool { return srv.served.Load() > 64 }, 100*time.Millisecond, time.Millisecond,
				"queued IDs must not start more nested runtimes while all connections are busy")
			unblock()
			return nil, work.Wait()
		},
	)
	require.NoError(t, err)
	require.Equal(t, int32(64), srv.peak.Load())
	require.Equal(t, int32(requests), srv.served.Load(), "every queued request must still run")
	require.Equal(t, int32(1), srv.closed.Load(), "the native transport still closes once")
}

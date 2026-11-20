package core

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"dagger.io/dagger/engineconn"
	"github.com/dagger/dagger/engine"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// Uses the existing built-engine integration harness, not the executor's fake
// setup seam. Running it requires the from-source engine archive and CLI and
// permission to start the privileged nested engine, its network and state cache.
// This is sampled topology/process-entry evidence only: the 50ms sample cannot
// establish distributed quiescence or exclude setup-delayed over-admission.
// NewServer-zero mutant discrimination still requires an executed result and a
// separately reviewed identity-bound admission frontier, not exec.run counts.
func (EngineSuite) TestConfiguredExecutionAdmissionAcrossSessions(ctx context.Context, t *testctx.T) {
	outer := connect(ctx, t)
	name := "execution-admission-" + rand.Text()
	ctr := devEngineContainer(outer,
		engineWithBkConfig(ctx, t, func(_ context.Context, _ *testctx.T, cfg bkconfig.Config) bkconfig.Config {
			cfg.Workers.OCI.MaxParallelism = 2
			return cfg
		}),
		func(ctr *core.Container) *core.Container { return ctr.WithEnvVariable(engine.DaggerNameEnv, name) },
	)
	upstream := devEngineContainerAsService(ctr)
	tunnel, err := core.NewQuery(outer).Host().Tunnel(upstream).Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, discardNestedEngine(ctx, nil, &upstream, &tunnel)) })
	endpoint, err := tunnel.Endpoint(ctx, core.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)
	// This existing helper forces two real CLI sessions, even when the test
	// itself is nested; plain Connect would reuse DAGGER_SESSION_PORT.
	first, _ := connectWithTrace(ctx, t, engineconn.Config{RunnerHost: endpoint})
	second, _ := connectWithTrace(ctx, t, engineconn.Config{RunnerHost: endpoint})
	for _, client := range []*dagger.Client{first, second} {
		actual, err := core.NewQuery(client).Engine().Name(ctx)
		require.NoError(t, err)
		require.Equal(t, name, actual, "session must reach the configured engine")
	}
	clients, err := core.NewQuery(first).Engine().Clients(ctx)
	require.NoError(t, err)
	require.Len(t, clients, 2, "two independent clients must coexist on this engine")
	require.NotEqual(t, clients[0], clients[1])

	entered := make(chan string, 3)
	release := make(chan struct{})
	var unblock sync.Once
	var active, peak atomic.Int32
	gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		entered <- r.URL.Path
		select {
		case <-release:
			fmt.Fprintln(w, "ok")
		case <-r.Context().Done():
		}
	}))
	defer gate.Close()
	// Open the gate before closing the HTTP server, including on assertion
	// failure, so cleanup never waits for its own blocked handler.
	defer unblock.Do(func() { close(release) })
	port := gate.Listener.Addr().(*net.TCPAddr).Port
	base := func(client *dagger.Client) *core.Container {
		service := core.NewQuery(client).Host().Service([]core.PortForward{{Backend: port, Frontend: 8080}})
		return core.NewQuery(client).Container().From(alpineImage).WithServiceBinding("gate", service)
	}
	firstBase, secondBase := base(first), base(second)
	for _, ctr := range []*core.Container{firstBase, secondBase} {
		_, err := ctr.Sync(ctx) // Resolve the image and binding before saturation.
		require.NoError(t, err)
	}
	run := func(ctr *core.Container, path string) error {
		out, err := ctr.WithExec([]string{"wget", "-qO-", "http://gate:8080/" + path}, core.ContainerWithExecOpts{DisableDaggerInDagger: true}).Stdout(ctx)
		if err == nil && out != "ok\n" {
			return fmt.Errorf("%s: unexpected gate response %q", path, out)
		}
		return err
	}
	holdersDone := make(chan error, 2)
	go func() { holdersDone <- run(firstBase, "holder-a") }()
	go func() { holdersDone <- run(secondBase, "holder-b") }()
	holders := map[string]bool{}
	for range 2 {
		select {
		case path := <-entered:
			holders[path] = true
		case err := <-holdersDone:
			t.Fatalf("holder returned before gate release: %v", err)
		case <-ctx.Done():
			t.Fatal(context.Cause(ctx))
		}
	}
	require.Equal(t, map[string]bool{"/holder-a": true, "/holder-b": true}, holders)
	liveDone := make(chan error, 1)
	go func() { liveDone <- run(firstBase, "live-waiter") }()
	// Sample process entry while holders remain gated. No identity-bound
	// queue frontier is available from connectWithTrace: BeginStep records
	// exec.waitParallelism only in native wcprof, never as an OTel span.
	// Passing this sample does not prove that the live goroutine has reached
	// admission, or discriminate the NewServer-zero mutant causally.
	select {
	case path := <-entered:
		t.Fatalf("configured per-engine bound exceeded: %s", path)
	case err := <-liveDone:
		t.Fatalf("live request returned before holder release: %v", err)
	case <-time.After(50 * time.Millisecond):
	case <-ctx.Done():
		t.Fatal(context.Cause(ctx))
	}
	unblock.Do(func() { close(release) })
	select {
	case path := <-entered:
		require.Equal(t, "/live-waiter", path, "live request must enter the process")
	case <-ctx.Done():
		t.Fatal(context.Cause(ctx))
	}
	for range 2 {
		select {
		case err := <-holdersDone:
			require.NoError(t, err)
		case <-ctx.Done():
			t.Fatal(context.Cause(ctx))
		}
	}
	select {
	case err := <-liveDone:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal(context.Cause(ctx))
	}
	require.Equal(t, int32(2), peak.Load())
}

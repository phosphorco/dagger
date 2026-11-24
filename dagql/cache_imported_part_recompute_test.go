package dagql_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

type importedPartServerBase interface{ core.Server }

type importedPartQueryServer struct {
	importedPartServerBase
	manager snapshots.SnapshotManager
}

func (s *importedPartQueryServer) SnapshotManager() snapshots.SnapshotManager { return s.manager }

func TestImportedOperationNoneDirectoryRecomputesExpiredOffer(t *testing.T) {
	for _, tc := range []struct {
		name          string
		expired       bool
		workers       int
		noServer      bool
		notReplayable bool
	}{
		{name: "expired offer", expired: true, workers: 1},
		{name: "expired offer concurrent demands", expired: true, workers: 8},
		{name: "usable offer", workers: 1},
		{name: "missing replay server refuses by name", expired: true, workers: 1, noServer: true},
		{name: "non replayable field is not run", expired: true, workers: 1, notReplayable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expired := tc.expired
			ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{ClientID: "test", SessionID: "test"})
			var runs atomic.Int64
			open := func(store *testutil.Store, counter *atomic.Int64) (*dagql.Cache, *dagql.Server, context.Context) {
				c, err := dagql.NewCache(ctx, "", store.Manager, nil)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
				root := &core.Query{Server: &importedPartQueryServer{manager: store.Manager}}
				srv := newExternalDagqlServerForTest(t, root)
				srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.Directory]{}))
				field := dagql.Func("recomputedDirectory", func(context.Context, *core.Query, struct{}) (*core.Directory, error) {
					if counter != nil {
						counter.Add(1)
					}
					ref, _ := store.Build(t, nil, "payload", "identical directory contents")
					d := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[snapshots.ImmutableRef, *core.Directory]), Platform: core.Platform{OS: "linux", Architecture: "amd64"}}
					d.SetPath("/")
					d.SetSnapshot(ref)
					return d, nil
				})
				if tc.notReplayable {
					field = field.NotReplayable("requires originating client")
				}
				dagql.Fields[*core.Query]{field}.Install(srv)
				return c, srv, core.ContextWithQuery(dagql.ContextWithServer(dagql.ContextWithCache(ctx, c), srv), root)
			}
			a, asrv, actx := open(testutil.NewStore(t), nil)
			b, bsrv, bctx := open(testutil.NewStore(t), &runs)
			original, err := asrv.Root().Select(actx, asrv, dagql.Selector{Field: "recomputedDirectory"})
			require.NoError(t, err)
			recipe, err := original.RecipeID(actx)
			require.NoError(t, err)
			selection := dagql.ValueSelection{Roots: []dagql.AnyResult{original}, Outputs: []dagql.SelectedValueOutput{{Result: original, Address: dagql.PersistedPartAddress{Part: "snapshot"}}}}
			require.NoError(t, a.WithExportedValues(actx, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, values *dagql.ExportedValues) error {
				blobs := map[string][]byte{}
				for _, chain := range values.Chains.Entries {
					for _, layer := range chain.Layers {
						data, err := content.ReadBlob(actx, chain.Provider, layer.Descriptor)
						require.NoError(t, err)
						blobs[layer.Descriptor.Digest.String()] = data
					}
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					data, ok := blobs[r.URL.Path[1:]]
					if !ok {
						http.NotFound(w, r)
						return
					}
					http.ServeContent(w, r, "layer", time.Time{}, bytes.NewReader(data))
				}))
				defer server.Close()
				bundle := values.Bundle
				require.NotEmpty(t, bundle.Outputs)
				for i := range bundle.Outputs {
					chain := bundle.Outputs[i].Chain
					require.NotNil(t, chain)
					require.NotEmpty(t, chain.Layers)
					chain.Addresses = map[digest.Digest]dagql.BlobAddress{}
					for _, layer := range chain.Layers {
						address := dagql.BlobAddress{URL: server.URL + "/" + layer.Descriptor.Digest.String()}
						if expired {
							address.ExpiresAtUnix = 1
						}
						chain.Addresses[layer.Descriptor.Digest] = address
					}
					chain.RenewalKey = ""
				}
				reply, err := b.MergeValues(bctx, dagql.CloudCacheID, bundle)
				require.NoError(t, err)
				imported, err := b.LoadResultByResultID(bctx, "test", bsrv, reply.Imported()[0].ResultID)
				require.NoError(t, err)
				recoveryCtx := bctx
				if tc.noServer {
					recoveryCtx = dagql.ContextWithServer(bctx, nil)
				}
				var demands errgroup.Group
				start := make(chan struct{})
				for range tc.workers {
					demands.Go(func() error { <-start; return b.EvaluateParts(recoveryCtx, imported, "snapshot") })
				}
				close(start)
				firstErr := demands.Wait()
				if tc.notReplayable {
					require.ErrorContains(t, firstErr, "ImportedPartRecoveryUnsupported (field recomputedDirectory is not replayable: requires originating client)")
					require.ErrorContains(t, firstErr, "candidate ")
					require.ErrorIs(t, firstErr, dagql.ErrUnavailablePart)
					_, err := bsrv.LoadReplayable(bctx, recipe)
					require.ErrorContains(t, err, "not replayable")
					require.Zero(t, runs.Load(), "neither automatic recovery nor explicit replay may run this field")
					next, err := bsrv.Root().Select(bctx, bsrv, dagql.Selector{Field: "recomputedDirectory"})
					require.NoError(t, err)
					oldID, err := imported.ID()
					require.NoError(t, err)
					newID, err := next.ID()
					require.NoError(t, err)
					require.NotEqual(t, oldID.EngineResultID(), newID.EngineResultID(), "a normal new call must miss the retired import")
					require.EqualValues(t, 1, runs.Load())
					return nil
				}
				if tc.noServer {
					require.ErrorContains(t, firstErr, "ImportedPartRecoveryUnsupported (no dagql server)")
					require.ErrorContains(t, firstErr, "candidate ")
					require.ErrorContains(t, firstErr, "offer exhausted for this demand")
					require.ErrorIs(t, firstErr, dagql.ErrUnavailablePart)
					firstErr = b.EvaluateParts(bctx, imported, "snapshot")
				}
				next, err := bsrv.LoadType(bctx, recipe)
				require.NoError(t, err)
				secondErr := b.EvaluateParts(bctx, next, "snapshot")
				t.Logf("first demand: %v; next lookup demand: %v; native calls: %d", firstErr, secondErr, runs.Load())
				oldID, err := imported.ID()
				require.NoError(t, err)
				newID, err := next.ID()
				require.NoError(t, err)
				if expired {
					require.NotEqual(t, oldID.EngineResultID(), newID.EngineResultID(), "the next lookup must not serve the unrecoverable imported row")
					require.EqualValues(t, 1, runs.Load(), "the call is recomputed once, not on every demand")
				} else {
					require.Equal(t, oldID.EngineResultID(), newID.EngineResultID())
					require.Zero(t, runs.Load(), "a usable offer must download without native recomputation")
				}
				require.NoError(t, firstErr)
				require.NoError(t, secondErr)
				return nil
			}))
		})
	}
}

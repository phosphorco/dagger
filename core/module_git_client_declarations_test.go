package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestGitClientDeclarationsShareSuccessfulRead(t *testing.T) {
	for _, value := range []string{"", "non-empty-declaration-digest"} {
		t.Run(value, func(t *testing.T) {
			memo := NewGitClientDeclarations()
			var reads atomic.Int64
			load := func(context.Context) (string, error) { reads.Add(1); return value, nil }
			var wg sync.WaitGroup
			failures := make(chan error, 20)
			for range 20 {
				wg.Go(func() {
					got, err := memo.get(t.Context(), gitClientDeclarationKey{source: 1}, load)
					if err != nil {
						failures <- err
						return
					}
					if got != value {
						failures <- errors.New("declaration digest changed")
					}
				})
			}
			wg.Wait()
			close(failures)
			for err := range failures {
				require.NoError(t, err)
			}
			require.EqualValues(t, 1, reads.Load())
		})
	}
}

func TestGitClientDeclarationsDoNotRetainFailedReads(t *testing.T) {
	memo := NewGitClientDeclarations()
	var reads int
	failure := errors.New("unreadable declaration")
	load := func(context.Context) (string, error) {
		reads++
		if reads < 3 {
			return "", failure
		}
		return "recovered", nil
	}
	key := gitClientDeclarationKey{source: 1}
	for range 2 {
		_, err := memo.get(t.Context(), key, load)
		require.ErrorIs(t, err, failure)
	}
	for range 2 {
		value, err := memo.get(t.Context(), key, load)
		require.NoError(t, err)
		require.Equal(t, "recovered", value)
	}
	require.Equal(t, 3, reads)
}

func TestGitClientDeclarationsSeparateSourcesHostsEpochsAndSessions(t *testing.T) {
	first, second := NewGitClientDeclarations(), NewGitClientDeclarations()
	reads := 0
	load := func(context.Context) (string, error) { reads++; return "digest", nil }
	keys := []gitClientDeclarationKey{{source: 1, host: "a", epoch: "1"}, {source: 2, host: "a", epoch: "1"}, {source: 1, host: "b", epoch: "1"}, {source: 1, host: "a", epoch: "2"}}
	for _, key := range keys {
		_, err := first.get(t.Context(), key, load)
		require.NoError(t, err)
	}
	_, err := second.get(t.Context(), keys[0], load)
	require.NoError(t, err)
	require.Equal(t, 5, reads)
}

func TestGitClientDeclarationsCancelledWaiterDoesNotCancelSharedRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		memo := NewGitClientDeclarations()
		key := gitClientDeclarationKey{source: 1}
		var reads atomic.Int64
		release := make(chan struct{})
		load := func(ctx context.Context) (string, error) {
			reads.Add(1)
			select {
			case <-release:
				return "digest", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		cancelled, cancel := context.WithCancel(context.Background())
		defer cancel()
		first := make(chan error, 1)
		go func() { _, err := memo.get(cancelled, key, load); first <- err }()
		synctest.Wait()
		second := make(chan error, 1)
		go func() {
			value, err := memo.get(context.Background(), key, load)
			if err == nil && value != "digest" {
				err = errors.New("declaration digest changed")
			}
			second <- err
		}()
		synctest.Wait()
		cancel()
		require.ErrorIs(t, <-first, context.Canceled)
		close(release)
		require.NoError(t, <-second)
		value, err := memo.get(context.Background(), key, load)
		require.NoError(t, err)
		require.Equal(t, "digest", value)
		require.EqualValues(t, 1, reads.Load())
	})
}

type gitDeclarationServer interface{ Server }

type unreadableGitDeclarationTestServer struct {
	gitDeclarationServer
	memo *GitClientDeclarations
}

func (s *unreadableGitDeclarationTestServer) ModuleParentHostClientMetadata(context.Context) (*engine.ClientMetadata, error) {
	return nil, errors.New("caller declaration is unreadable")
}

func (s *unreadableGitDeclarationTestServer) SessionGitClientDeclarations(context.Context) (*GitClientDeclarations, error) {
	return s.memo, nil
}

func TestGitClientDeclarationsUnreadableDoesNotFailOrReuse(t *testing.T) {
	f := newModuleOwnershipTest(t)
	ctx := f.session("unreadable-git-declaration")
	mod := f.module(ctx, "Unreadable", moduleOwnershipModuleOpts{})
	src := mod.Self().Source.Value.Self()
	src.Kind = ModuleSourceKindLocal
	src.Local = &LocalModuleSource{ContextDirectoryPath: "/unreadable"}
	f.root.Server = &unreadableGitDeclarationTestServer{gitDeclarationServer: f.root.Server, memo: NewGitClientDeclarations()}
	fn := &ModuleFunction{mod: mod}
	first, err := fn.declaredGitClientsDigest(ctx)
	require.NoError(t, err)
	second, err := fn.declaredGitClientsDigest(ctx)
	require.NoError(t, err)
	require.Contains(t, first, "unreadable:")
	require.Contains(t, second, "unreadable:")
	require.NotEqual(t, first, second)
}

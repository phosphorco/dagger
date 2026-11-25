package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dagger/dagger/dagql"
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

type gitDeclarationValueTestServer struct {
	gitDeclarationServer
	memo *GitClientDeclarations
}

func (s *gitDeclarationValueTestServer) SessionGitClientDeclarations(context.Context) (*GitClientDeclarations, error) {
	return s.memo, nil
}

func TestGitClientDeclarationsMemoPreservesFunctionBoundaryValue(t *testing.T) {
	const config = `[modules.go]
source = "go"
[sdks.go]
module = "go"
[sdks.go.scopes."modules/caller"]
is-module = true
clients = ["github.com/acme/private/hello@main", "https://github.com/acme/other.git/lib"]
`
	for _, tc := range []struct {
		name, config string
		unreadable   bool
	}{
		{name: "empty", config: ""},
		{name: "nonempty", config: config},
		{name: "unreadable", unreadable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newModuleOwnershipTest(t)
			ctx := f.session("declaration-value")
			ExistsTypes.Install(f.dag)
			f.dag.InstallObject(dagql.NewClass(f.dag, dagql.ClassOpts[*Directory]{Typed: &Directory{}}))
			f.dag.InstallObject(dagql.NewClass(f.dag, dagql.ClassOpts[*File]{Typed: &File{}}))
			dagql.Fields[*Directory]{
				dagql.Func("exists", func(_ context.Context, _ *Directory, args struct {
					Path         string
					ExpectedType dagql.Optional[ExistsType]
				}) (bool, error) {
					if tc.unreadable {
						return false, errors.New("declaration unavailable")
					}
					return args.Path == "dagger.toml", nil
				}),
				dagql.Func("file", func(_ context.Context, _ *Directory, _ struct{ Path string }) (*File, error) { return &File{}, nil }),
			}.Install(f.dag)
			dagql.Fields[*File]{
				dagql.Func("contents", func(context.Context, *File, struct{}) (string, error) { return tc.config, nil }),
			}.Install(f.dag)
			tree := newTypeDefAttachedResult(t, ctx, f.cache, f.dag, "declaration-tree", &Directory{})
			mod := f.module(ctx, "DeclarationValue", moduleOwnershipModuleOpts{})
			src := mod.Self().Source.Value.Self()
			src.SourceRootSubpath = "modules/caller"
			src.DirSrc = &DirModuleSource{OriginalContextDir: tree}
			source := newTypeDefAttachedResult(t, ctx, f.cache, f.dag, "declaration-source", src)
			mod.Self().Source = dagql.NonNull(source)
			_, err := moduleGitClientDeclarationKey(ctx, source)
			require.NoError(t, err)
			server := &gitDeclarationValueTestServer{gitDeclarationServer: f.root.Server}
			f.root.Server = server
			fn := &ModuleFunction{mod: mod}
			direct, err := fn.declaredGitClientsDigest(ctx)
			require.NoError(t, err)
			server.memo = NewGitClientDeclarations()
			first, err := fn.declaredGitClientsDigest(ctx)
			require.NoError(t, err)
			second, err := fn.declaredGitClientsDigest(ctx)
			require.NoError(t, err)
			if tc.unreadable {
				for _, value := range []string{direct, first, second} {
					require.Contains(t, value, "unreadable:")
				}
				require.NotEqual(t, direct, first)
				require.NotEqual(t, direct, second)
				require.NotEqual(t, first, second)
			} else {
				require.Equal(t, direct, first)
				require.Equal(t, direct, second)
				if tc.name == "empty" {
					require.Empty(t, direct)
				} else {
					require.NotEmpty(t, direct)
				}
			}
		})
	}
}

func TestGitClientDeclarationsBoundUnreadableSelections(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%t", shared), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newModuleOwnershipTest(t)
				ctx, cancel := context.WithCancel(f.session("bounded-declaration"))
				defer cancel()
				ExistsTypes.Install(f.dag)
				f.dag.InstallObject(dagql.NewClass(f.dag, dagql.ClassOpts[*Directory]{Typed: &Directory{}}))
				f.dag.InstallObject(dagql.NewClass(f.dag, dagql.ClassOpts[*File]{Typed: &File{}}))
				var reads atomic.Int64
				dagql.Fields[*Directory]{
					dagql.Func("exists", func(_ context.Context, _ *Directory, args struct {
						Path         string
						ExpectedType dagql.Optional[ExistsType]
					}) (bool, error) {
						return args.Path == "dagger.toml", nil
					}),
					dagql.Func("file", func(context.Context, *Directory, struct{ Path string }) (*File, error) { return &File{}, nil }),
				}.Install(f.dag)
				dagql.Fields[*File]{
					dagql.Func("contents", func(ctx context.Context, _ *File, _ struct{}) (string, error) {
						if reads.Add(1) <= 2 {
							<-ctx.Done()
							return "", ctx.Err()
						}
						return "[modules.go]\nsource = \"go\"\n[sdks.go]\nmodule = \"go\"\n[sdks.go.scopes.\"modules/caller\"]\nis-module = true\nclients = [\"github.com/acme/private/hello@main\"]\n", nil
					}),
				}.Install(f.dag)
				tree := newTypeDefAttachedResult(t, ctx, f.cache, f.dag, "bounded-declaration-tree", &Directory{})
				mod := f.module(ctx, "BoundedDeclaration", moduleOwnershipModuleOpts{})
				src := mod.Self().Source.Value.Self()
				src.SourceRootSubpath = "modules/caller"
				src.DirSrc = &DirModuleSource{OriginalContextDir: tree}
				mod.Self().Source = dagql.NonNull(newTypeDefAttachedResult(t, ctx, f.cache, f.dag, "bounded-declaration-source", src))
				server := &gitDeclarationValueTestServer{gitDeclarationServer: f.root.Server}
				if shared {
					server.memo = NewGitClientDeclarations()
				}
				f.root.Server = server
				fn := &ModuleFunction{mod: mod}
				var previous string
				for range 2 {
					result := make(chan string, 2)
					go func() {
						value, err := fn.declaredGitClientsDigest(ctx)
						if err != nil {
							result <- "error:" + err.Error()
							return
						}
						result <- value
					}()
					synctest.Wait()
					waiters := 1
					if shared {
						time.Sleep(5 * time.Second)
						go func() {
							value, err := fn.declaredGitClientsDigest(ctx)
							if err != nil {
								result <- "error:" + err.Error()
								return
							}
							result <- value
						}()
						synctest.Wait()
						waiters++
						time.Sleep(5 * time.Second)
					} else {
						time.Sleep(10 * time.Second)
					}
					synctest.Wait()
					for range waiters {
						select {
						case value := <-result:
							require.Contains(t, value, "unreadable:")
							require.NotEqual(t, previous, value)
							previous = value
						default:
							cancel()
							<-result
							t.Fatal("a blocked declaration read held a selection beyond ten seconds")
						}
					}
				}
				value, err := fn.declaredGitClientsDigest(ctx)
				require.NoError(t, err)
				require.NotEmpty(t, value)
				require.NotContains(t, value, "unreadable:")
				again, err := fn.declaredGitClientsDigest(ctx)
				require.NoError(t, err)
				require.Equal(t, value, again)
				require.EqualValues(t, 3, reads.Load())
			})
		})
	}
}

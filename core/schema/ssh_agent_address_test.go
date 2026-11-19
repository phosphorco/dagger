package schema

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/buildkit/session/sshforward"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh/agent"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type sshIdentityTestAgent struct {
	sshforward.UnimplementedSSHServer
	agent agent.Agent
}

func (s *sshIdentityTestAgent) CheckAgent(context.Context, *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	return &sshforward.CheckAgentResponse{}, nil
}
func (s *sshIdentityTestAgent) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	relay, local := net.Pipe()
	defer relay.Close()
	go func() { defer local.Close(); _ = agent.ServeAgent(s.agent, local) }()
	return sshforward.Copy(stream.Context(), relay, stream, nil)
}

func newSSHIdentityAttachable(t *testing.T) *grpc.ClientConn {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	ring := agent.NewKeyring()
	require.NoError(t, ring.Add(agent.AddedKey{PrivateKey: key}))
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	sshforward.RegisterSSHServer(server, &sshIdentityTestAgent{agent: ring})
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:bufnet", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	conn.Connect()
	t.Cleanup(func() { _ = conn.Close(); server.Stop(); _ = listener.Close() })
	return conn
}

type sshIdentityTestServer struct{ *llmAuthSchemaServer }

func (s *sshIdentityTestServer) SpecificClientAttachableConn(_ context.Context, client string, options core.SpecificClientAttachableConnOpts) (*grpc.ClientConn, bool, error) {
	conn, ok := s.attachables[client]
	if !ok && !options.IfAvailable {
		return nil, false, fmt.Errorf("no active session attachables for client %q", client)
	}
	return conn, ok, nil
}

func newSSHIdentitySession(t *testing.T, cache *dagql.Cache, session string, conn *grpc.ClientConn) (context.Context, *dagql.Server) {
	t.Helper()
	md := &engine.ClientMetadata{SessionID: session, ClientID: session + "-client", SSHAuthSocketPath: "/ambient/agent-unavailable"}
	attachables := map[string]*grpc.ClientConn{}
	if conn != nil {
		attachables[md.ClientID] = conn
	}
	srv := &sshIdentityTestServer{llmAuthSchemaServer: &llmAuthSchemaServer{currentTypeDefsTestServer: &currentTypeDefsTestServer{mainClient: md, platform: core.Platform{OS: "linux", Architecture: "amd64"}}, parent: md, attachables: attachables}}
	ctx := dagql.ContextWithCache(engine.ContextWithClientMetadata(t.Context(), md), cache)
	root := core.NewRoot(srv)
	ctx = core.ContextWithQuery(ctx, root)
	base, err := NewCoreSchemaBase(ctx, srv)
	require.NoError(t, err)
	dag, err := base.Fork(ctx, root, "")
	require.NoError(t, err)
	srv.dag = dag
	srv.deps = core.NewSchemaBuilder(root, nil)
	return ctx, dag
}

func TestSSHAgentAddressReusesDependentCallsAcrossSessions(t *testing.T) {
	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	sameAgent := newSSHIdentityAttachable(t)
	changedAgent := newSSHIdentityAttachable(t)
	var executions atomic.Int32
	invoke := func(session, path string, conn *grpc.ClientConn) (dagql.SessionResourceHandle, int) {
		ctx, dag := newSSHIdentitySession(t, cache, session, conn)
		dagql.Fields[*core.Socket]{dagql.Func("executionProbe", func(context.Context, *core.Socket, struct{}) (int, error) { return int(executions.Add(1)), nil })}.Install(dag)
		var socket dagql.ObjectResult[*core.Socket]
		require.NoError(t, dag.Select(ctx, dag.Root(), &socket, dagql.Selector{Field: "address", Args: []dagql.NamedInput{{Name: "value", Value: dagql.NewString("ssh-agent://" + path)}}}, dagql.Selector{Field: "socket"}))
		require.Equal(t, core.SocketKindSSHHandle, socket.Self().Kind)
		location, err := socket.Self().URL(ctx)
		require.NoError(t, err)
		require.Equal(t, "unix://"+path, location, "reuse must bind the current caller endpoint")
		var result int
		require.NoError(t, dag.Select(ctx, socket, &result, dagql.Selector{Field: "executionProbe"}))
		return socket.Self().Handle, result
	}
	firstHandle, first := invoke("first", "/first-agent.sock", sameAgent)
	repeatedHandle, repeated := invoke("second", "/another-agent.sock", sameAgent)
	require.Equal(t, firstHandle, repeatedHandle)
	require.Equal(t, first, repeated, "same SSH authority must reuse the dependent computation")
	changedHandle, changed := invoke("third", "/another-agent.sock", changedAgent)
	require.NotEqual(t, firstHandle, changedHandle)
	require.NotEqual(t, first, changed, "another SSH authority must not reuse it")
}

func TestSSHAgentAddressCannotUseAnotherSessionsBinding(t *testing.T) {
	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx, dag := newSSHIdentitySession(t, cache, "authorized", newSSHIdentityAttachable(t))
	var socket dagql.ObjectResult[*core.Socket]
	selectors := []dagql.Selector{{Field: "address", Args: []dagql.NamedInput{{Name: "value", Value: dagql.NewString("ssh-agent:///shared/path.sock")}}}, {Field: "socket"}}
	require.NoError(t, dag.Select(ctx, dag.Root(), &socket, selectors...))
	unbound, other := newSSHIdentitySession(t, cache, "unbound", nil)
	err = other.Select(unbound, other.Root(), &socket, selectors...)
	require.ErrorContains(t, err, "failed to get SSH auth socket fingerprints")
}

func TestSSHAgentAddressRejectsMalformedLocations(t *testing.T) {
	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx, dag := newSSHIdentitySession(t, cache, "parse", nil)
	for _, address := range []string{"ssh-agent:relative", "ssh-agent://host/path", "ssh-agent://", "ssh-agent:///tmp/agent?key=x", "ssh-agent:///tmp/agent?", "ssh-agent:///tmp/agent#x"} {
		t.Run(address, func(t *testing.T) {
			var socket dagql.ObjectResult[*core.Socket]
			err := dag.Select(ctx, dag.Root(), &socket, dagql.Selector{Field: "address", Args: []dagql.NamedInput{{Name: "value", Value: dagql.NewString(address)}}}, dagql.Selector{Field: "socket"})
			require.ErrorContains(t, err, "SSH-agent address")
		})
	}
}

func TestGitRetainsDelegatedSSHAgentIdentity(t *testing.T) {
	e := newTransferTestEngine(t, []byte("ssh-agent-test"))
	t.Cleanup(func() { require.NoError(t, e.cache.Close(context.Background())) })
	owner := &engine.ClientMetadata{SessionID: "delegation", ClientID: "owner"}
	e.srv.mainClient = owner
	e.srv.parent = owner
	e.srv.attachables[owner.ClientID] = newSSHIdentityAttachable(t)
	ctx := engine.ContextWithClientMetadata(e.base, owner)
	var socket dagql.ObjectResult[*core.Socket]
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &socket, dagql.Selector{Field: "address", Args: []dagql.NamedInput{{Name: "value", Value: dagql.NewString("ssh-agent:///owner/agent.sock")}}}, dagql.Selector{Field: "socket"}))
	id, err := socket.ID()
	require.NoError(t, err)
	var executions atomic.Int32
	dagql.Fields[*core.GitRepository]{dagql.Func("executionProbe", func(context.Context, *core.GitRepository, struct{}) (int, error) { return int(executions.Add(1)), nil })}.Install(e.dag)
	var results []int
	for _, client := range []string{"first-consumer", "second-consumer"} {
		consumer := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{SessionID: owner.SessionID, ClientID: client})
		var repo dagql.ObjectResult[*core.GitRepository]
		require.NoError(t, e.dag.Select(consumer, e.dag.Root(), &repo, dagql.Selector{Field: "git", Args: []dagql.NamedInput{{Name: "url", Value: dagql.NewString("ssh://git@example.invalid/repository.git")}, {Name: "sshAuthSocket", Value: dagql.Opt(dagql.NewID[*core.Socket](id))}}}))
		var result int
		require.NoError(t, e.dag.Select(consumer, repo, &result, dagql.Selector{Field: "executionProbe"}))
		results = append(results, result)
	}
	require.Equal(t, results[0], results[1], "delegating the same scoped capability must not create a new auth identity")
	var opaque dagql.ObjectResult[*core.Socket]
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &opaque, dagql.Selector{Field: "host"}, dagql.Selector{Field: "unixSocket", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString("/owner/raw-agent.sock")}}}))
	opaqueID, err := opaque.ID()
	require.NoError(t, err)
	var rawResults []int
	for _, client := range []string{"raw-first", "raw-second"} {
		consumer := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{SessionID: owner.SessionID, ClientID: client})
		var repo dagql.ObjectResult[*core.GitRepository]
		require.NoError(t, e.dag.Select(consumer, e.dag.Root(), &repo, dagql.Selector{Field: "git", Args: []dagql.NamedInput{{Name: "url", Value: dagql.NewString("ssh://git@example.invalid/repository.git")}, {Name: "sshAuthSocket", Value: dagql.Opt(dagql.NewID[*core.Socket](opaqueID))}}}))
		var result int
		require.NoError(t, e.dag.Select(consumer, repo, &result, dagql.Selector{Field: "executionProbe"}))
		rawResults = append(rawResults, result)
	}
	require.NotEqual(t, rawResults[0], rawResults[1], "opaque sockets must retain nested-client isolation")
	ungranted := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{SessionID: owner.SessionID, ClientID: "ungranted"})
	var repository dagql.ObjectResult[*core.GitRepository]
	require.NoError(t, e.dag.Select(ungranted, e.dag.Root(), &repository, dagql.Selector{Field: "git", Args: []dagql.NamedInput{{Name: "url", Value: dagql.NewString("ssh://git@example.invalid/repository.git")}}}))
	var ungrantedResult int
	require.NoError(t, e.dag.Select(ungranted, repository, &ungrantedResult, dagql.Selector{Field: "executionProbe"}))
	require.NotEqual(t, results[0], ungrantedResult, "a constructor without the socket grant must not share the authenticated result")

}

func TestSSHAgentScopeRejectsSourceAndPathTogether(t *testing.T) {
	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx, dag := newSSHIdentitySession(t, cache, "arguments", nil)
	var opaque dagql.ObjectResult[*core.Socket]
	require.NoError(t, dag.Select(ctx, dag.Root(), &opaque, dagql.Selector{Field: "host"}, dagql.Selector{Field: "unixSocket", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString("/explicit/agent.sock")}}}))
	id, err := opaque.ID()
	require.NoError(t, err)
	var scoped dagql.ObjectResult[*core.Socket]
	err = dag.Select(ctx, dag.Root(), &scoped, dagql.Selector{Field: "host"}, dagql.Selector{Field: "_sshAuthSocket", Args: []dagql.NamedInput{{Name: "source", Value: dagql.Opt(dagql.NewID[*core.Socket](id))}, {Name: "path", Value: dagql.NewString("/another/agent.sock")}}})
	require.ErrorContains(t, err, "source or a path, not both")
}

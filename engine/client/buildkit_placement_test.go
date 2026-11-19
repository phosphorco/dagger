package client

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	controlapi "github.com/dagger/dagger/internal/buildkit/api/services/control"
	apitypes "github.com/dagger/dagger/internal/buildkit/api/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type placementInfoServer struct {
	controlapi.UnimplementedControlServer
}

func (s *placementInfoServer) Info(ctx context.Context, _ *controlapi.InfoRequest) (*controlapi.InfoResponse, error) {
	worker := "preflight-worker"
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		values := md.Get(engine.ClientMetadataMetaKey)
		if len(values) != 0 {
			if len(values) != 1 {
				return nil, status.Error(codes.InvalidArgument, "duplicate native metadata")
			}
			h := http.Header{}
			h.Set(engine.ClientMetadataMetaKey, values[0])
			client, err := engine.ClientMetadataFromHTTPHeaders(h)
			if err != nil || client.SessionID != "native-session" {
				return nil, status.Error(codes.InvalidArgument, "wrong native session metadata")
			}
			if values := md.Get("existing-header"); len(values) != 1 || values[0] != "retained" {
				return nil, status.Error(codes.InvalidArgument, "outgoing metadata lost")
			}
			worker = "session-worker"
		}
	}
	return &controlapi.InfoResponse{BuildkitVersion: &apitypes.BuildkitVersion{Package: engine.Package, Revision: worker}}, nil
}

type placementInfoConnector struct{ listener *bufconn.Listener }

func (c placementInfoConnector) Connect(ctx context.Context) (net.Conn, error) {
	return c.listener.DialContext(ctx)
}

func (placementInfoConnector) EngineID() string { return "" }

func TestBuildkitStartupUsesNativeSessionMetadata(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	controlapi.RegisterControlServer(server, &placementInfoServer{})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		listener.Close()
		require.NoError(t, <-done)
	})
	for _, hasMetadata := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy preflight", true: "native session"}[hasMetadata], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			ctx = metadata.AppendToOutgoingContext(ctx, "existing-header", "retained")
			want := "preflight-worker"
			if hasMetadata {
				ctx = metadata.AppendToOutgoingContext(ctx, engine.ClientMetadataMetaKey, "stale-metadata")
				ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{SessionID: "native-session"})
				want = "session-worker"
			}
			client, info, err := newBuildkitClient(ctx, &url.URL{Scheme: "tcp", Host: "placement"}, placementInfoConnector{listener})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Close()) })
			require.Equal(t, want, info.BuildkitVersion.Revision)
		})
	}
}

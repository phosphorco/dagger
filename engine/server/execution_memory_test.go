package server

import (
	"testing"

	"github.com/dagger/dagger/engine/config"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/stretchr/testify/require"
)

func TestExecutionMemoryInvalidConfigurationFailsBeforeStartup(t *testing.T) {
	server, err := NewServer(t.Context(), &NewServerOpts{
		Config: &config.Config{Execution: &config.ExecutionConfig{MemoryMaxBytes: -1}}, BuildkitConfig: &bkconfig.Config{},
	})
	require.Nil(t, server)
	require.ErrorContains(t, err, "nonnegative")
}

package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExecutionMemoryConfigurationIsOptionalAndNumeric(t *testing.T) {
	old, err := Load(strings.NewReader(`{}`))
	require.NoError(t, err)
	require.Nil(t, old.Execution)
	bounded, err := Load(strings.NewReader(`{"execution":{"memoryMaxBytes":25769803776}}`))
	require.NoError(t, err)
	require.Equal(t, int64(24<<30), bounded.Execution.MemoryMaxBytes)
	_, err = Load(strings.NewReader(`{"execution":{"memoryMaxBytes":"24GiB"}}`))
	require.Error(t, err)
}

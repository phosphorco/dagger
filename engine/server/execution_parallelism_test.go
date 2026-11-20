package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExecutionParallelismConfiguration(t *testing.T) {
	unlimited, err := newExecutionParallelism(0)
	require.NoError(t, err)
	require.Nil(t, unlimited)
	invalid, err := newExecutionParallelism(-1)
	require.ErrorContains(t, err, "nonnegative")
	require.Nil(t, invalid)
	bound, err := newExecutionParallelism(2)
	require.NoError(t, err)
	require.True(t, bound.TryAcquire(2))
	require.False(t, bound.TryAcquire(1))
	bound.Release(2)
	require.True(t, bound.TryAcquire(1))
	bound.Release(1)
}

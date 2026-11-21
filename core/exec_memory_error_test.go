package core

import (
	"errors"
	"fmt"
	"testing"

	"github.com/dagger/dagger/engine/engineutil"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

type unreadableMemoryMeta struct{ bkcache.ImmutableRef }

func TestExecutionMemoryFaultIsNotProjectedAsAProgramExitResult(t *testing.T) {
	for _, fault := range []error{
		&engineutil.ExecutionMemoryError{ExecutionID: "probe", LimitBytes: 32 << 20, LocalOOM: 1},
		fmt.Errorf("wrapped: %w", &engineutil.ExecutionMemoryError{ExecutionID: "probe", LimitBytes: 32 << 20, ObservationError: errors.New("missing own cgroup evidence")}),
	} {
		result, projected, err := execErrorFromMetaRef(t.Context(), nil, trace.SpanContext{}, fault, nil, unreadableMemoryMeta{})
		require.NoError(t, err)
		require.False(t, projected)
		require.Nil(t, result)
	}
	require.Panics(t, func() {
		_, _, _ = execErrorFromMetaRef(t.Context(), nil, trace.SpanContext{}, errors.New("ordinary execution error"), nil, unreadableMemoryMeta{})
	})
}

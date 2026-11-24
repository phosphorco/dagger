package dagql

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImportedPartRetirementPreservesRecoveryAndCleanupClasses(t *testing.T) {
	retired := &importedPartRetired{cause: fmt.Errorf("snapshot: %w", ErrUnavailablePart)}
	require.ErrorIs(t, retired, ErrUnavailablePart)
	require.ErrorIs(t, retired, ErrPartReselect)
	require.True(t, partCanReselect(retired))
	require.True(t, partCanReselect(fmt.Errorf("outer: %w", retired)))
	require.False(t, partCanReselect(errors.Join(retired, errors.New("cleanup failed"))))
}

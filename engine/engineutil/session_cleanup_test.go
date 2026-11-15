package engineutil

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSessionCleanupAccountingWaitsThroughCleanupAndPermitRelease(t *testing.T) {
	client, _ := admissionClients(t, 1)
	client.EnableSessionCleanupAccounting()
	state := admissionState("cleanup-proof")
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	state.cleanups.Add("held native cleanup", func() error { close(entered); <-release; return nil })
	go func() { done <- client.run(context.Background(), state) }()
	<-entered
	// The native running map and done channel have already changed, but neither
	// the executor cleanup nor its permit has reached its owning terminal point.
	require.Error(t, client.ConfirmSessionResourceCleanup())
	require.False(t, client.ParallelismSem.TryAcquire(1))
	close(release)
	require.NoError(t, <-done)
	require.NoError(t, client.ConfirmSessionResourceCleanup())
	_, lateErr := client.GetGitConfig(context.Background(), "late-request")
	require.ErrorContains(t, lateErr, "resource owner is closed")
	require.True(t, client.ParallelismSem.TryAcquire(1))
	client.ParallelismSem.Release(1)
}
func TestSessionCleanupAccountingRetainsFailureAndUnknownHelpers(t *testing.T) {
	client, _ := admissionClients(t, 1)
	client.EnableSessionCleanupAccounting()
	state := admissionState("failed-cleanup")
	state.cleanups.Add("native cleanup failure", func() error { return errors.New("cleanup failed") })
	require.Error(t, client.run(context.Background(), state))
	require.Error(t, client.ConfirmSessionResourceCleanup())
	helper, _ := admissionClients(t, 1)
	helper.EnableSessionCleanupAccounting()
	_, err := helper.NewNetworkNamespace(context.Background(), "no-provider")
	require.Error(t, err)
	require.Error(t, helper.ConfirmSessionResourceCleanup(), "unobserved helper cleanup cannot become a false zero")
}

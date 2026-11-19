package client

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dagger/dagger/engine"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/stretchr/testify/require"
	"net/http"
)

func TestPlacementReceiptIsScopedToCheckoutAndEndpoint(t *testing.T) {
	key := checkoutPlacementKey("tcp://pool", "checkout-one")
	require.Equal(t, key, checkoutPlacementKey("tcp://pool", "checkout-one"))
	require.NotEqual(t, key, checkoutPlacementKey("tcp://pool", "checkout-two"))
	require.NotEqual(t, key, checkoutPlacementKey("tcp://other-pool", "checkout-one"))
	require.Empty(t, checkoutPlacementKey("tcp://pool", ""))
	store, err := openCheckoutReceipt(t.TempDir(), key)
	require.NoError(t, err)
	defer store.Close()
	_, err = store.read()
	require.ErrorIs(t, err, fs.ErrNotExist)
	require.NoError(t, store.remember("b3-hel"))
	worker, err := store.read()
	require.NoError(t, err)
	require.Equal(t, "b3-hel", worker)
}

func TestPlacementReceiptNeverPublishesPartialConcurrentWrites(t *testing.T) {
	store, err := openCheckoutReceipt(t.TempDir(), checkoutPlacementKey("tcp://pool", "checkout"))
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, store.remember("first"))
	var group sync.WaitGroup
	failures := make(chan error, 512)
	for _, worker := range []string{"b1-hel", "b2-hel", "b3-hel", "b4-hel"} {
		group.Add(1)
		go func() {
			defer group.Done()
			for range 20 {
				if err := store.remember(worker); err != nil {
					failures <- err
				}
			}
		}()
	}
	for range 100 {
		worker, err := store.read()
		if err != nil {
			failures <- err
		}
		if worker == "" {
			failures <- errors.New("empty receipt visible")
		}
	}
	group.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
}

func TestPlacementReceiptRejectsInvalidAndEscapingData(t *testing.T) {
	for _, data := range []string{"{", `{"version":2,"worker":"b3-hel"}`, `{"version":1,"worker":""}`, `{"version":1,"worker":"bad\nworker"}`} {
		dir := t.TempDir()
		key := checkoutPlacementKey("tcp://pool", "checkout")
		require.NoError(t, os.WriteFile(filepath.Join(dir, key), []byte(data), 0600))
		store, err := openCheckoutReceipt(dir, key)
		require.NoError(t, err)
		_, err = store.read()
		require.Error(t, err)
		require.NoError(t, store.Close())
	}
	_, err := openCheckoutReceipt(t.TempDir(), "../escape")
	require.Error(t, err)
}

func TestClientMetadataCarriesReceiptWithoutMutatingOriginalLabels(t *testing.T) {
	source := map[string]string{"dagger.io/git.worktree": "checkout", "preserved": "value"}
	client := &Client{Params: Params{SessionID: "native-session"}, labels: enginetel.NewLabels(source, nil, nil), preferredWorker: "b3-hel", preferenceStatus: "prior_assignment"}
	metadata := client.clientMetadata()
	wire := metadata.AppendToHTTPHeaders(http.Header{})
	decoded, err := engine.ClientMetadataFromHTTPHeaders(wire)
	require.NoError(t, err)
	require.Equal(t, "b3-hel", decoded.Labels["dagger.io/engine.last"])
	require.Equal(t, "prior_assignment", decoded.Labels["dagger.io/engine.last.status"])
	require.Equal(t, "value", decoded.Labels["preserved"])
	require.NotContains(t, source, "dagger.io/engine.last")
	metadata.Labels["preserved"] = "changed"
	require.Equal(t, "value", source["preserved"])
}

func TestPlacementReceiptDoesNotWriteIntoCurrentDirectory(t *testing.T) {
	for _, parent := range []string{"", "relative-state"} {
		_, err := openCheckoutReceipt(parent, checkoutPlacementKey("tcp://pool", "checkout"))
		require.Error(t, err)
	}
}

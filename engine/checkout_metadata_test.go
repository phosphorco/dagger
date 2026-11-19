package engine

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestCheckoutPreferenceWireFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/checkout-preference.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]string
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 2 {
		t.Fatalf("expected two native sessions, got %d", len(fixtures))
	}
	for _, session := range []string{"native-session", "next-session"} {
		metadata := ClientMetadata{
			SessionID: session,
			Labels: map[string]string{
				"dagger.io/git.worktree":       strings.Repeat("a", 64),
				"dagger.io/engine.last":        "b",
				"dagger.io/engine.last.status": "prior_assignment",
			},
		}
		got := metadata.AppendToHTTPHeaders(http.Header{}).Get(ClientMetadataMetaKey)
		if got != fixtures[session] {
			t.Fatalf("native wire fixture changed for %s", session)
		}
	}
}

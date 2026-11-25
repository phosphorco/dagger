package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type ModuleGitClientReadsSuite struct{}

func TestModuleGitClientReads(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ModuleGitClientReadsSuite{})
}

func (ModuleGitClientReadsSuite) TestSelectionsShareDeclarationRead(ctx context.Context, t *testctx.T) {
	strace, err := exec.LookPath("strace")
	require.NoError(t, err, "client-read regression requires strace")
	root := t.TempDir()
	evidence := t.TempDir()
	module := filepath.Join(root, "dagger", "read-probe")
	require.NoError(t, os.MkdirAll(module, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dagger.toml"), []byte("[modules.read-probe]\nsource = \"dagger/read-probe\"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(module, "dagger-module.toml"), []byte("name = \"read-probe\"\nengineVersion = \"v1.0.0-beta.23\"\n[runtime]\nsource = \"dang\"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(module, "main.dang"), []byte("type ReadProbe { pub echo(value: String!): String! { value } }\n"), 0o600))
	reads := func(count int) int {
		selections := make([]string, count)
		expected := make(map[string]string, count)
		for i := range count {
			key := fmt.Sprintf("q%d", i)
			value := fmt.Sprintf("%d", i)
			selections[i] = fmt.Sprintf("%s: echo(value: %q)", key, value)
			expected[key] = value
		}
		query := filepath.Join(evidence, fmt.Sprintf("query-%d.graphql", count))
		require.NoError(t, os.WriteFile(query, []byte("{ readProbe { "+strings.Join(selections, " ")+" } }"), 0o600))
		trace := filepath.Join(evidence, fmt.Sprintf("client-reads-%d.strace", count))
		cmd := hostDaggerCommandRaw(ctx, t, root, "--workspace="+root, "api", "query", "--doc="+query)
		cmd.Env = nil
		for _, entry := range os.Environ() {
			key, _, _ := strings.Cut(entry, "=")
			switch key {
			case "DAGGER_MODULE", "DAGGER_SESSION_PORT", "DAGGER_SESSION_TOKEN", "DAGGER_SESSION_CLIENT_ID":
				continue
			}
			cmd.Env = append(cmd.Env, entry)
		}
		cmd.Args = append([]string{strace, "-f", "-s", "1024", "-e", "trace=newfstatat,statx,openat", "-o", trace}, cmd.Args...)
		cmd.Path = strace
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		require.NoError(t, cmd.Run(), stderr.String())
		var response struct {
			ReadProbe map[string]string `json:"readProbe"`
		}
		require.NoError(t, json.Unmarshal([]byte(stdout.String()), &response), stdout.String())
		require.Equal(t, expected, response.ReadProbe)
		raw, err := os.ReadFile(trace)
		require.NoError(t, err)
		opened := 0
		for line := range strings.SplitSeq(string(raw), "\n") {
			if strings.Contains(line, "openat(") && strings.Contains(line, "dagger.toml\"") && !strings.Contains(line, "= -1") {
				opened++
			}
		}
		require.Positive(t, opened, "the oracle must observe actual client declaration reads")
		t.Logf("selections=%d declarationReads=%d evidence=%s", count, opened, trace)
		return opened
	}
	single, many := reads(1), reads(20)
	require.LessOrEqual(t, many, single+2, "additional module selections must not repeat the client declaration walk")
}

package engineutil

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	runc "github.com/containerd/go-runc"
	"github.com/dagger/dagger/internal/buildkit/executor"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
)

func TestExecutionMemoryCeilingIsPrivateAndDoesNotDependOnTheCommandName(t *testing.T) {
	for _, command := range []string{"/tsgo", "/different-program"} {
		t.Run(command, func(t *testing.T) {
			client := &Client{Opts: &Opts{ExecutionMemoryMaxBytes: 24 << 30}}
			state := &execState{
				id: "memory-ceiling-probe",
				procInfo: &executor.ProcessInfo{Meta: executor.Meta{
					Args: []string{command}, Cwd: "/",
				}},
				networkNamespace: &noopNetworkNamespace{},
				cleanups:         &cleanups.Cleanups{},
			}
			t.Cleanup(func() { require.NoError(t, state.cleanups.Run()) })
			require.NoError(t, client.generateBaseSpec(t.Context(), state))
			require.Equal(t, "/exec/memory-ceiling-probe/container", state.spec.Linux.CgroupsPath)
			require.NotNil(t, state.spec.Linux.Resources.Memory)
			require.NotNil(t, state.spec.Linux.Resources.Memory.Limit)
			require.Equal(t, int64(24<<30), *state.spec.Linux.Resources.Memory.Limit)
			require.Equal(t, "0", state.spec.Linux.Resources.Unified["memory.swap.max"])
			require.Equal(t, "1", state.spec.Linux.Resources.Unified["memory.oom.group"])
		})
	}
}

func TestExecutionMemoryAdmissionDistinguishesLocalOOMFromReclaimAndOtherKills(t *testing.T) {
	for _, probe := range []struct {
		name, events, fault string
		exit                int
	}{
		{"normal", "max 0\noom 0\noom_kill 0\n", "", 0},
		{"successful reclaim", "max 118\noom 0\noom_kill 0\n", "", 0},
		{"ancestor kill", "max 0\noom 0\noom_kill 1\n", "", 137},
		{"local exhaustion", "max 5\noom 1\noom_kill 1\n", "ExecutionMemoryBoundExceeded", 137},
		{"local failure despite native zero", "max 5\noom 1\noom_kill 0\n", "ExecutionMemoryBoundExceeded", 0},
		{"duplicate evidence", "max 5\noom 1\noom 0\noom_kill 1\n", "ExecutionMemoryBoundObservationUnavailable", 0},
		{"missing evidence", "max 5\noom_kill 1\n", "ExecutionMemoryBoundObservationUnavailable", 137},
	} {
		t.Run(probe.name, func(t *testing.T) {
			directory := t.TempDir()
			for name, value := range map[string]string{
				"memory.max": "33554432", "memory.swap.max": "0", "memory.oom.group": "1", "memory.events.local": probe.events,
			} {
				require.NoError(t, os.WriteFile(filepath.Join(directory, name), []byte(value), 0o600))
			}
			state := &execState{id: "proof", exitCodePath: filepath.Join(t.TempDir(), "exit"), procInfo: &executor.ProcessInfo{Meta: executor.Meta{ValidExitCodes: []int{0, 137}}}}
			var native error
			if probe.exit != 0 {
				native = &runc.ExitError{Status: probe.exit}
			}
			err := completeExecutionMemoryBound(t.Context(), state, native, 32<<20, directory)
			if probe.fault == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, probe.fault)
			}
			data, readErr := os.ReadFile(state.exitCodePath)
			require.NoError(t, readErr)
			require.Equal(t, strconv.Itoa(probe.exit), string(data))
		})
	}
}

func TestExecutionMemoryDefaultAndInvalidConfiguration(t *testing.T) {
	_, err := NewOpts(Opts{ExecutionMemoryMaxBytes: -1})
	require.ErrorContains(t, err, "nonnegative")
	state := &execState{id: "old-default", procInfo: &executor.ProcessInfo{}}
	require.NoError(t, completeExecutionMemoryBound(t.Context(), state, nil, 0, "/not-a-cgroup"))
	require.Nil(t, executionMemoryRuntimeArgs(0))
}

func TestExecutionMemoryFaultCannotBeAcceptedAsAToleratedExit(t *testing.T) {
	directory := t.TempDir()
	for name, value := range map[string]string{
		"memory.max": "33554432", "memory.swap.max": "0", "memory.oom.group": "1",
		"memory.events.local": "low 0\nhigh 0\nmax 5\noom 1\noom_kill 1\noom_group_kill 1\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(directory, name), []byte(value), 0o600))
	}
	state := &execState{id: "bounded-command", exitCodePath: filepath.Join(t.TempDir(), "exit"), procInfo: &executor.ProcessInfo{Meta: executor.Meta{ValidExitCodes: []int{137}}}}
	err := completeExecutionMemoryBound(t.Context(), state, &runc.ExitError{Status: 137}, 32<<20, directory)
	var fault *ExecutionMemoryError
	require.ErrorAs(t, err, &fault)
	require.Equal(t, uint64(1), fault.LocalOOM)
	require.ErrorContains(t, err, "ExecutionMemoryBoundExceeded")
	data, readErr := os.ReadFile(state.exitCodePath)
	require.NoError(t, readErr)
	require.Equal(t, "137", string(data))
}

func TestExecutionMemoryPreservesStricterLimitsAndKernelPageGranularity(t *testing.T) {
	for _, probe := range []struct {
		name                 string
		configured, existing int64
		unified              string
		expected             int64
	}{
		{"stricter OCI limit", 24 << 30, 12 << 30, "", 12 << 30},
		{"stricter unified limit", 24 << 30, 12 << 30, "8589934592", 8 << 30},
		{"kernel page granularity", int64(os.Getpagesize()*16 + 1), 0, "", int64(os.Getpagesize() * 16)},
	} {
		t.Run(probe.name, func(t *testing.T) {
			resources := &specs.LinuxResources{Memory: &specs.LinuxMemory{}}
			if probe.existing > 0 {
				resources.Memory.Limit = &probe.existing
			}
			if probe.unified != "" {
				resources.Unified = map[string]string{"memory.max": probe.unified}
			}
			state := &execState{spec: &specs.Spec{Linux: &specs.Linux{CgroupsPath: "/exec/private/container", Resources: resources}}}
			client := &Client{Opts: &Opts{ExecutionMemoryMaxBytes: probe.configured}}
			require.NoError(t, client.setExecutionMemoryCeiling(state))
			require.Equal(t, probe.expected, *state.spec.Linux.Resources.Memory.Limit)
			require.Equal(t, "0", state.spec.Linux.Resources.Unified["memory.swap.max"])
		})
	}
	t.Run("smaller than one page", func(t *testing.T) {
		state := &execState{spec: &specs.Spec{Linux: &specs.Linux{CgroupsPath: "/exec/private/container"}}}
		client := &Client{Opts: &Opts{ExecutionMemoryMaxBytes: 1}}
		require.ErrorContains(t, client.setExecutionMemoryCeiling(state), "at least one kernel page")
	})
}

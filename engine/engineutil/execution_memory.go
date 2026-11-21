package engineutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/opencontainers/runtime-spec/specs-go"
)

type ExecutionMemoryError struct {
	ExecutionID      string
	LimitBytes       int64
	LocalOOM         uint64
	ObservationError error
	Err              error
}

func executionMemoryRuntimeArgs(limit int64) []string {
	if limit == 0 {
		return nil
	}
	return []string{"--keep"}
}

func (e *ExecutionMemoryError) Error() string {
	if e.ObservationError != nil {
		return fmt.Sprintf("ExecutionMemoryBoundObservationUnavailable: execution %s, ceiling %d bytes: %v; native execution error: %v", e.ExecutionID, e.LimitBytes, e.ObservationError, e.Err)
	}
	return fmt.Sprintf("ExecutionMemoryBoundExceeded: execution %s reached its %d-byte memory ceiling (local OOM events: %d)", e.ExecutionID, e.LimitBytes, e.LocalOOM)
}

func (e *ExecutionMemoryError) Unwrap() error {
	return errors.Join(e.Err, e.ObservationError)
}

func completeExecutionMemoryBound(ctx context.Context, state *execState, nativeError error, limit int64, directory string) error {
	result := exitError(ctx, state.exitCodePath, nativeError, state.procInfo.Meta.ValidExitCodes)
	if limit == 0 {
		return result
	}
	oom, err := readExecutionMemoryOOM(directory, limit)
	if err != nil || oom > 0 {
		return &ExecutionMemoryError{ExecutionID: state.id, LimitBytes: limit, LocalOOM: oom, ObservationError: err, Err: nativeError}
	}
	return result
}

func readExecutionMemoryOOM(directory string, limit int64) (uint64, error) {
	for _, field := range []struct{ name, expected string }{
		{"memory.max", strconv.FormatInt(limit, 10)}, {"memory.swap.max", "0"}, {"memory.oom.group", "1"},
	} {
		data, err := os.ReadFile(filepath.Join(directory, field.name))
		if err != nil {
			return 0, err
		}
		if strings.TrimSpace(string(data)) != field.expected {
			return 0, fmt.Errorf("unexpected %s: expected %s, observed %q", field.name, field.expected, strings.TrimSpace(string(data)))
		}
	}
	data, err := os.ReadFile(filepath.Join(directory, "memory.events.local"))
	if err != nil {
		return 0, err
	}
	events := map[string]uint64{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return 0, fmt.Errorf("invalid memory.events.local line")
		}
		if _, exists := events[fields[0]]; exists {
			return 0, fmt.Errorf("duplicate memory.events.local field %s", fields[0])
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, err
		}
		events[fields[0]] = value
	}
	for _, key := range []string{"max", "oom", "oom_kill"} {
		if _, exists := events[key]; !exists {
			return 0, fmt.Errorf("memory.events.local lacks %s", key)
		}
	}
	return events["oom"], nil
}

func (c *Client) setExecutionMemoryCeiling(state *execState) error {
	limit := c.ExecutionMemoryMaxBytes
	if limit < 0 {
		return fmt.Errorf("execution memoryMaxBytes must be nonnegative")
	}
	if limit == 0 {
		return nil
	}
	linux := state.spec.Linux
	if linux == nil || !filepath.IsAbs(linux.CgroupsPath) || filepath.Clean(linux.CgroupsPath) != linux.CgroupsPath || strings.Contains(linux.CgroupsPath, ":") || linux.CgroupsPath == "/" {
		return fmt.Errorf("execution memory ceiling requires a private cgroup v2 filesystem path")
	}
	if linux.Resources == nil {
		linux.Resources = &specs.LinuxResources{}
	}
	resources := linux.Resources
	if resources.Memory == nil {
		resources.Memory = &specs.LinuxMemory{}
	}
	if resources.Memory.Limit != nil && *resources.Memory.Limit > 0 {
		limit = min(limit, *resources.Memory.Limit)
	}
	if resources.Unified == nil {
		resources.Unified = map[string]string{}
	}
	if value, exists := resources.Unified["memory.max"]; exists && value != "max" {
		existing, err := strconv.ParseInt(value, 10, 64)
		if err != nil || existing <= 0 {
			return fmt.Errorf("invalid existing execution memory.max %q", value)
		}
		limit = min(limit, existing)
	}
	page := int64(os.Getpagesize())
	limit = limit / page * page
	if limit == 0 {
		return fmt.Errorf("execution memory ceiling must allow at least one kernel page")
	}
	delete(resources.Unified, "memory.max")
	disabled := false
	resources.Memory.Limit = &limit
	resources.Memory.Swap = nil
	resources.Memory.DisableOOMKiller = &disabled
	resources.Unified["memory.swap.max"] = "0"
	resources.Unified["memory.oom.group"] = "1"
	return nil
}

package server

import (
	"fmt"

	"golang.org/x/sync/semaphore"
)

// The existing OCI setting owns one no-child execution bound shared by every
// session through the shallow-copied engineutil.Opts. Zero remains unlimited.
// The executor excludes services and commands that retain Dagger API access.
func newExecutionParallelism(maxParallelism int) (*semaphore.Weighted, error) {
	if maxParallelism < 0 {
		return nil, fmt.Errorf("OCI max-parallelism must be nonnegative")
	}
	if maxParallelism == 0 {
		return nil, nil
	}
	return semaphore.NewWeighted(int64(maxParallelism)), nil
}

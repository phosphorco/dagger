// Command cost for this explicit offline proof only. It creates no collector,
// listener, lifecycle owner or environment capture.
package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 3 {
		os.Exit(2)
	}
	began := time.Now()
	cmd := exec.Command(os.Args[2], os.Args[3:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	result := struct {
		Command          []string `json:"command"`
		WallSeconds      float64  `json:"wallSeconds"`
		UserSeconds      float64  `json:"userSeconds"`
		SystemSeconds    float64  `json:"systemSeconds"`
		CommandMaxRSSKiB int64    `json:"commandMaxRSSKiB"`
		Exit             int      `json:"exit"`
		Error            string   `json:"error,omitempty"`
	}{Command: os.Args[2:], WallSeconds: time.Since(began).Seconds(), Exit: 1}
	if cmd.ProcessState != nil {
		result.Exit = cmd.ProcessState.ExitCode()
		result.UserSeconds = cmd.ProcessState.UserTime().Seconds()
		result.SystemSeconds = cmd.ProcessState.SystemTime().Seconds()
		if usage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			result.CommandMaxRSSKiB = usage.Maxrss
		}
	}
	if err != nil {
		result.Error = err.Error()
	}
	data, marshalErr := json.Marshal(result)
	if marshalErr != nil || os.WriteFile(os.Args[1], append(data, '\n'), 0600) != nil {
		os.Exit(2)
	}
	if result.Exit < 0 {
		os.Exit(1)
	}
	os.Exit(result.Exit)
}

//go:build !linux

package external

import "os/exec"

// Non-Linux development retains CommandContext's direct-child cancellation.
func configureProcess(cmd *exec.Cmd) {}

//go:build linux

package external

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// The MVP runs on Linux. Cancel the module's process group as well as its
// parent, so SDK helper processes do not survive a disabled connector.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

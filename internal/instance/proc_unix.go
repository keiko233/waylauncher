//go:build unix

package instance

import (
	"os/exec"
	"syscall"
)

// The transport runs in its own process group so that ssh and any helpers
// die with it.
func setProcessGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func killGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, sig)
	}
}

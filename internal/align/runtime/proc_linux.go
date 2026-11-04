//go:build linux

package runtime

import (
	"os/exec"
	"syscall"
)

// setAttrs puts the worker in its own process group and has the kernel kill it when the server dies.
// Closing its stdin ends it too, so a missed signal does not leave a worker behind.
func setAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL, Setpgid: true}
}

// killGroup kills the worker and anything it started.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}

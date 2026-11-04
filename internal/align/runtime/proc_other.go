//go:build !linux

package runtime

import "os/exec"

func setAttrs(cmd *exec.Cmd) {}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

//go:build !unix

package sandbox

import "os/exec"

func configureCommandProcessGroup(_ *exec.Cmd) {}

func killCommandProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

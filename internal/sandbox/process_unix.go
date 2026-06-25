//go:build unix

package sandbox

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func configureCommandProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killCommandProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	for _, pid := range descendantPIDs(cmd.Process.Pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func descendantPIDs(pid int) []int {
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, field := range strings.Fields(string(out)) {
		child, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		pids = append(pids, descendantPIDs(child)...)
		pids = append(pids, child)
	}
	return pids
}

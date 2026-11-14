//go:build linux

package runtime

import (
	"os/exec"
	"syscall"
)

// launch pins its goroutine to the creating OS thread until the child exits.
// Parent-death SIGTERM stops new worker claims even when Ext kills the guest.
func configureChild(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGTERM
}

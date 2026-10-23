//go:build !unix

package runtime

import (
	"os"
	"os/exec"
)

func configureProcess(command *exec.Cmd) {}

func killProcessGroup(command *exec.Cmd) error {
	if command.Process == nil {
		return os.ErrProcessDone
	}
	return command.Process.Kill()
}

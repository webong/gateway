package provisioning

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type commandRunner interface {
	Output(context.Context, []byte, string, ...string) (string, error)
}

type execCommandRunner struct{}

func (execCommandRunner) Output(ctx context.Context, input []byte, binary string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, binary, arguments...)
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return "", err
		}
		return "", fmt.Errorf("%w: %s", err, message)
	}

	return strings.TrimSpace(string(output)), nil
}

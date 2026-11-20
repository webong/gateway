package provision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"strings"
)

type fakeLogger struct{}

func (*fakeLogger) Debug(string, ...interface{}) {}
func (*fakeLogger) Info(string, ...interface{})  {}
func (*fakeLogger) Warn(string, ...interface{})  {}
func (*fakeLogger) Error(string, ...interface{}) {}

type recoveryWorkload struct {
	port       int
	image      string
	name       string
	executable string
	arguments  []string
}

func (w *recoveryWorkload) Type() string {
	if w.name != "" {
		return w.name
	}
	return "test"
}
func (*recoveryWorkload) Supports(driver string) bool { return driver == DriverDocker }
func (*recoveryWorkload) Validate(ServerSpec) error   { return nil }
func (w *recoveryWorkload) Launch(ServerSpec, string, string, int) (LaunchSpec, error) {
	image := w.image
	if image == "" {
		image = "server:v1"
	}
	executable := w.executable
	if executable == "" {
		executable = "server"
	}
	return LaunchSpec{Image: image, Port: w.port, Executable: executable, Arguments: w.arguments}, nil
}

type recordingCommandRunner struct {
	outputs []string
	calls   []string
}

func (r *recordingCommandRunner) Output(_ context.Context, _ []byte, binary string, arguments ...string) (string, error) {
	r.calls = append(r.calls, strings.Join(append([]string{binary}, arguments...), " "))
	if len(r.outputs) == 0 {
		return "", nil
	}
	output := r.outputs[0]
	r.outputs = r.outputs[1:]
	return output, nil
}

// Discover the resource from its published ownership label instead of recreating its name.
func runtimeResourceName(id string) string {
	output, _ := exec.Command("docker", "ps", "--all", "--quiet", "--no-trunc", "--filter", "label=workload.provisioning.instance="+id).Output()
	return strings.TrimSpace(string(output))
}
func runtimeLabelValue(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:56]
}

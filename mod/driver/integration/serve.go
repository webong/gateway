package integration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/webong/gateway/driver"
)

// Serve handles one JSON request per line on stdin and writes one response per
// line to stdout. Extensions must write diagnostics to stderr only.
func Serve(workload driver.Workload, input io.Reader, output io.Writer) error {
	if workload == nil || strings.TrimSpace(workload.Type()) == "" {
		return fmt.Errorf("integration requires a named workload")
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), MaxMessageBytes)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		var request Request
		response := Response{Version: Version}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			response.Error = "invalid integration request"
		} else {
			response.ID = request.ID
			response = handle(workload, request)
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func handle(workload driver.Workload, request Request) Response {
	response := Response{Version: Version, ID: request.ID}
	if request.Version != Version {
		response.Error = "unsupported integration protocol version"
		return response
	}
	switch request.Method {
	case "describe":
		response.Type = workload.Type()
		for _, candidate := range []string{driver.DriverLocal, driver.DriverDocker, driver.DriverKubernetes} {
			if workload.Supports(candidate) {
				response.Drivers = append(response.Drivers, candidate)
			}
		}
	case "validate":
		if request.Spec == nil {
			response.Error = "server specification is required"
		} else if err := workload.Validate(*request.Spec); err != nil {
			response.Error = err.Error()
		}
	case "launch":
		if request.Spec == nil {
			response.Error = "server specification is required"
		} else {
			launch, err := workload.Launch(*request.Spec, request.Runtime, request.Host, request.Port)
			if err != nil {
				response.Error = err.Error()
			} else {
				response.Launch = &launch
			}
		}
	default:
		response.Error = "unknown integration method"
	}
	return response
}

// Encode is shared by tests and hosts that need to validate line size before
// writing a request to an integration.
func Encode(request Request) ([]byte, error) {
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if len(data)+1 > MaxMessageBytes {
		return nil, fmt.Errorf("integration request exceeds %d bytes", MaxMessageBytes)
	}
	return append(data, '\n'), nil
}

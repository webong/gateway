package integration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ServeProvisioner handles one JSON request per line on stdin and writes one
// response per line to stdout. Extensions must write diagnostics to stderr only.
func ServeProvisioner(extension Provisioner, input io.Reader, output io.Writer) error {
	if extension == nil || strings.TrimSpace(extension.Type()) == "" {
		return fmt.Errorf("integration requires a named extension")
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), ProvisionMaxMessageBytes)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		var request ProvisionRequest
		response := ProvisionResponse{Version: ProvisionVersion}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			response.Error = "invalid integration request"
		} else {
			response.ID = request.ID
			response = handleProvision(extension, request)
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func handleProvision(extension Provisioner, request ProvisionRequest) ProvisionResponse {
	response := ProvisionResponse{Version: ProvisionVersion, ID: request.ID}
	if request.Version != ProvisionVersion {
		response.Error = "unsupported integration protocol version"
		return response
	}
	switch request.Method {
	case "describe":
		response.Type = extension.Type()
		for _, candidate := range []string{DriverLocal, DriverDocker, DriverKubernetes} {
			if extension.Supports(candidate) {
				response.Drivers = append(response.Drivers, candidate)
			}
		}
	case "validate":
		if request.Spec == nil {
			response.Error = "server specification is required"
		} else if err := extension.Validate(*request.Spec); err != nil {
			response.Error = err.Error()
		}
	case "launch":
		if request.Spec == nil {
			response.Error = "server specification is required"
		} else {
			launch, err := extension.Launch(*request.Spec, request.Runtime, request.Host, request.Port)
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

// EncodeProvisionRequest is shared by tests and hosts that need to validate
// line size before writing a request to an integration provisioner.
func EncodeProvisionRequest(request ProvisionRequest) ([]byte, error) {
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if len(data)+1 > ProvisionMaxMessageBytes {
		return nil, fmt.Errorf("integration request exceeds %d bytes", ProvisionMaxMessageBytes)
	}
	return append(data, '\n'), nil
}

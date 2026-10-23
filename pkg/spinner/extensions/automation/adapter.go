// Package automationextensions provides Spinner's host-side automation plugin
// registry. It depends only on the automation driver contract and the portable
// plugin contract; concrete engines are guests and are never imported here.
package automationextensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/host"
	"github.com/webong/gateway/driver/automation"
)

// AutomationContract identifies the Gateway automation plugin contract.
var AutomationContract = contract.ContractRef{Name: "gateway.automation", Version: "v1"}

// ExecuteRequest is the payload for the gateway.automation/v1 execute operation.
type ExecuteRequest struct {
	Language  string         `json:"language"`
	Source    string         `json:"source"`
	Event     map[string]any `json:"event"`
	Variables map[string]any `json:"variables"`
}

// ExecuteResponse is the result of the gateway.automation/v1 execute operation.
type ExecuteResponse struct {
	Outcome automation.Outcome `json:"outcome"`
}

// EngineAdapter exposes an in-process automation.Engine as a plugin.Endpoint.
type EngineAdapter struct {
	engine automation.Engine
	desc   contract.Descriptor
}

func NewEngineAdapter(e automation.Engine, desc contract.Descriptor) *EngineAdapter {
	return &EngineAdapter{engine: e, desc: desc}
}

func (a *EngineAdapter) Handshake(context.Context) (contract.Descriptor, error) {
	return a.desc, nil
}

func (a *EngineAdapter) Invoke(ctx context.Context, req contract.Request) (contract.Response, error) {
	var in ExecuteRequest
	if err := json.Unmarshal(req.Payload, &in); err != nil {
		return remoteError(req.ID, "invalid_payload", err), nil
	}
	out, err := a.engine.Execute(ctx, in.Source, automation.Input{Event: in.Event, Variables: in.Variables})
	if err != nil {
		return remoteError(req.ID, "execution_failed", err), nil
	}
	payload, err := json.Marshal(ExecuteResponse{Outcome: out})
	if err != nil {
		return remoteError(req.ID, "encode_failed", err), nil
	}
	return contract.Response{APIVersion: contract.APIVersion, ID: req.ID, Payload: payload}, nil
}

// Registry is the process-local catalog of guest engines, keyed by language.
// It stores plugin.Endpoints so in-process, native and external guests are all
// managed through one interface.
type Registry struct {
	engines  map[string]automation.Engine
	endpoint map[string]contract.Endpoint
}

func NewRegistry() *Registry {
	return &Registry{
		engines:  make(map[string]automation.Engine),
		endpoint: make(map[string]contract.Endpoint),
	}
}

func (r *Registry) Register(adapter *EngineAdapter) {
	lang := string(adapter.engine.Language())
	r.engines[lang] = adapter.engine
	r.endpoint[lang] = adapter
}

func (r *Registry) Get(lang automation.Language) (automation.Engine, bool) {
	e, ok := r.engines[string(lang)]
	return e, ok
}

func (r *Registry) All() []automation.Engine {
	result := make([]automation.Engine, 0, len(r.engines))
	for _, e := range r.engines {
		result = append(result, e)
	}
	return result
}

func (r *Registry) AsEndpoints() []contract.Endpoint {
	result := make([]contract.Endpoint, 0, len(r.endpoint))
	for _, e := range r.endpoint {
		result = append(result, e)
	}
	return result
}

// EngineDescriptor declares an engine as a gateway.automation/v1 guest.
func EngineDescriptor(engine automation.Engine, id contract.Identity) contract.Descriptor {
	return contract.Descriptor{
		APIVersion: contract.APIVersion,
		Identity:   id,
		Contracts: []contract.Contract{{
			ContractRef: AutomationContract,
			Operations:  []contract.Operation{{Name: "execute", Surface: string(engine.Language())}},
		}},
	}
}

// NewEndpointEngine wraps a plugin.Endpoint guest (native .so or in-process)
// as an automation.Engine, deriving the language from the handshake.
func NewEndpointEngine(ctx context.Context, endpoint contract.Endpoint) (automation.Engine, error) {
	desc, err := endpoint.Handshake(ctx)
	if err != nil {
		return nil, err
	}
	lang, err := engineLanguage(desc)
	if err != nil {
		return nil, err
	}
	return &endpointEngine{endpoint: endpoint, identity: desc.Identity, lang: lang}, nil
}

// NewProcessEngine wraps an external plugin process as an automation.Engine.
func NewProcessEngine(_ context.Context, proc *host.Process, desc contract.Descriptor) (automation.Engine, error) {
	lang, err := engineLanguage(desc)
	if err != nil {
		return nil, err
	}
	return &processEngine{process: proc, lang: lang}, nil
}

type endpointEngine struct {
	endpoint contract.Endpoint
	identity contract.Identity
	lang     automation.Language
}

func (e *endpointEngine) Language() automation.Language { return e.lang }

func (e *endpointEngine) Execute(ctx context.Context, source string, input automation.Input) (automation.Outcome, error) {
	payload, err := json.Marshal(ExecuteRequest{
		Language:  string(e.lang),
		Source:    source,
		Event:     input.Event,
		Variables: input.Variables,
	})
	if err != nil {
		return automation.Outcome{}, err
	}
	req := contract.Request{
		APIVersion: contract.APIVersion,
		ID:         strconv.FormatInt(time.Now().UnixNano(), 10),
		Plugin:     e.identity,
		Contract:   AutomationContract,
		Operation:  "execute",
		Surface:    string(e.lang),
		Deadline:   time.Now().Add(30 * time.Second),
		Payload:    payload,
	}
	resp, err := e.endpoint.Invoke(ctx, req)
	if err != nil {
		return automation.Outcome{}, err
	}
	if resp.Error != nil {
		return automation.Outcome{}, errors.New(resp.Error.Code + ": " + resp.Error.Message)
	}
	var out ExecuteResponse
	if err := json.Unmarshal(resp.Payload, &out); err != nil {
		return automation.Outcome{}, err
	}
	return out.Outcome, nil
}

type processEngine struct {
	process *host.Process
	lang    automation.Language
}

func (e *processEngine) Language() automation.Language { return e.lang }

func (e *processEngine) Execute(ctx context.Context, source string, input automation.Input) (automation.Outcome, error) {
	var out ExecuteResponse
	err := e.process.Call(ctx, "gateway.automation.execute", ExecuteRequest{
		Language:  string(e.lang),
		Source:    source,
		Event:     input.Event,
		Variables: input.Variables,
	}, &out)
	if err != nil {
		return automation.Outcome{}, err
	}
	return out.Outcome, nil
}

// engineLanguage returns the declared language for the guest's execute
// operation, or an error when the descriptor is not an automation guest.
func engineLanguage(desc contract.Descriptor) (automation.Language, error) {
	for _, c := range desc.Contracts {
		if c.ContractRef != AutomationContract {
			continue
		}
		for _, op := range c.Operations {
			if op.Name == "execute" && op.Surface != "" {
				return automation.Language(op.Surface), nil
			}
		}
	}
	return "", fmt.Errorf("%w: descriptor does not declare gateway.automation/v1 execute", contract.ErrUnsupported)
}

// SupportsAutomation reports whether a descriptor declares the automation contract.
func SupportsAutomation(desc contract.Descriptor) bool {
	for _, c := range desc.Contracts {
		if c.ContractRef == AutomationContract {
			return true
		}
	}
	return false
}

func remoteError(id, code string, err error) contract.Response {
	return contract.Response{
		APIVersion: contract.APIVersion,
		ID:         id,
		Error:      &contract.RemoteError{Code: code, Message: err.Error()},
	}
}

var _ contract.Endpoint = (*EngineAdapter)(nil)
var _ automation.Engine = (*endpointEngine)(nil)
var _ automation.Engine = (*processEngine)(nil)

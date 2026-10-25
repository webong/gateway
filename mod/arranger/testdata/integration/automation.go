package example

import (
	"context"

	gatewayautomation "github.com/webong/gateway/arranger/automation"
)

type AutomationEngine struct{}

func (AutomationEngine) Language() gatewayautomation.Language { return "example" }

func (AutomationEngine) Execute(_ context.Context, _ string, input gatewayautomation.Input) (gatewayautomation.Outcome, error) {
	return gatewayautomation.Outcome{
		Variables: input.Variables,
		Response:  &gatewayautomation.Response{Status: 202, Body: "extension"},
	}, nil
}

var _ gatewayautomation.Engine = AutomationEngine{}

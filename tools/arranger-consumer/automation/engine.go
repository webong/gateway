package automation

import (
	"context"

	arrangerautomation "github.com/webong/gateway/arranger/automation"
)

type Engine struct{}

func (Engine) Language() arrangerautomation.Language { return "example" }

func (Engine) Execute(_ context.Context, _ string, input arrangerautomation.Input) (arrangerautomation.Outcome, error) {
	return arrangerautomation.Outcome{
		Variables: input.Variables,
		Response:  &arrangerautomation.Response{Status: 202, Body: "extension"},
	}, nil
}

var _ arrangerautomation.Engine = Engine{}

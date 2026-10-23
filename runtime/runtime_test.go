package runtime

import (
	"context"
	"testing"

	"github.com/webong/gateway/driver/automation"
)

func TestLuaCanUseTheGatewayCapabilities(t *testing.T) {
	outcome, err := automation.NewRuntime(New()).Execute(context.Background(), automation.Script{
		Language: Language,
		Source:   `gateway.set("value", gateway.event.value); gateway.respond("ok", 204)`,
	}, automation.Input{Event: map[string]any{"value": "copied"}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Variables["value"] != "copied" || outcome.Response == nil || outcome.Response.Status != 204 {
		t.Fatalf("unexpected outcome %#v", outcome)
	}
}

func TestLuaCanDeclareASchedule(t *testing.T) {
	outcome, err := automation.NewRuntime(New()).Execute(context.Background(), automation.Script{
		Language: Language,
		Source:   `gateway.schedule({key = "daily-report", name = "Daily report", cron = "0 9 * * *", language = "lua", source = "gateway.respond('done', 200)", payload = {team = "ops"}})`,
	}, automation.Input{})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Schedules) != 1 || outcome.Schedules[0].Key != "daily-report" || outcome.Schedules[0].Payload["team"] != "ops" {
		t.Fatalf("unexpected schedules %#v", outcome.Schedules)
	}
}

func TestLuaCannotOpenHostLibraries(t *testing.T) {
	outcome, err := automation.NewRuntime(New()).Execute(context.Background(), automation.Script{
		Language: Language,
		Source:   `if io == nil and os == nil and package == nil then gateway.respond("hidden", 200) else gateway.respond("exposed", 200) end`,
	}, automation.Input{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Response == nil || outcome.Response.Body != "hidden" {
		t.Fatalf("unexpected host capability exposure: %#v", outcome.Response)
	}
}

package integrationextensions

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	arranger "github.com/webong/gateway/arranger/integration"
)

type stopProbe struct {
	declared bool
	fail     bool
	bounded  bool
	calls    []string
}

func (p *stopProbe) Descriptor() plugin.Descriptor {
	ops := []plugin.Operation{{Name: arranger.OperationStart, Surface: "spinner"}}
	if p.declared {
		ops = append(ops, plugin.Operation{Name: arranger.OperationStop, Surface: "spinner"})
	}
	return plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/worker", Revision: "r1"}, Contracts: []plugin.Contract{{ContractRef: arranger.RuntimeContract, Operations: ops}}}
}
func (p *stopProbe) Call(ctx context.Context, method string, params, result any) error {
	p.calls = append(p.calls, method)
	deadline, ok := ctx.Deadline()
	p.bounded = ok && time.Until(deadline) <= 10*time.Second
	if !p.bounded {
		return errors.New("missing bounded stop deadline")
	}
	if p.fail {
		return errors.New("stop failed")
	}
	return nil
}
func (p *stopProbe) Close() error { p.calls = append(p.calls, "close"); return nil }
func TestRuntimeCloseStopsDeclaredChildrenBeforeKillingGuest(t *testing.T) {
	for _, fail := range []bool{false, true} {
		probe := &stopProbe{declared: true, fail: fail}
		process := &runtimeProcess{extensionProcess: probe}
		_ = process.Close()
		_ = process.Close()
		if !probe.bounded {
			t.Fatal("stop must have a bounded deadline")
		}
		if !reflect.DeepEqual(probe.calls, []string{plugin.Method(arranger.RuntimeContract, arranger.OperationStop), "close"}) {
			t.Fatalf("close order: %v", probe.calls)
		}
	}
	probe := &stopProbe{}
	_ = (&runtimeProcess{extensionProcess: probe}).Close()
	if !reflect.DeepEqual(probe.calls, []string{"close"}) {
		t.Fatalf("legacy runtime gained a stop call: %v", probe.calls)
	}
}

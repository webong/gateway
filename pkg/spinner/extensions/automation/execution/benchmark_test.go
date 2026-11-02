package execution

import (
	"context"
	"testing"
	"time"

	"github.com/webong/gateway/arranger/automation"
)

type benchmarkDelegate struct{}

func (benchmarkDelegate) Supports(automation.Language) bool { return true }
func (benchmarkDelegate) Execute(context.Context, automation.Script, automation.Input) (automation.Outcome, error) {
	return automation.Outcome{}, nil
}

// BenchmarkExecutionModes compares the local scheduling overhead under the
// same worker, queue, and deadline settings. It does not model guest I/O.
func BenchmarkExecutionModes(b *testing.B) {
	for _, mode := range []Mode{ModeGoroutine, ModeErgo} {
		b.Run(string(mode), func(b *testing.B) {
			executor, err := New(benchmarkDelegate{}, Options{
				Mode: mode, Workers: 8, QueueSize: 32, Timeout: 10 * time.Second,
			})
			if err != nil {
				b.Fatal(err)
			}
			defer executor.Close()
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if _, err := executor.Execute(context.Background(), automation.Script{}, automation.Input{}); err != nil {
						b.Error(err)
					}
				}
			})
		})
	}
}

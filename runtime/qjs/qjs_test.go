package qjs

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRunUsesFreshContextForEachInvocation(t *testing.T) {
	for number := range 25 {
		input := []byte(fmt.Sprintf(`{"number":%d}`, number))
		output, err := Run(context.Background(), `(input => JSON.stringify({number: input.number}))`, input, 8*1024*1024, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if string(output) != string(input) {
			t.Fatalf("unexpected output: %s", output)
		}
	}
}

func TestRunDoesNotLoadHostModules(t *testing.T) {
	_, err := Run(context.Background(), `import * as os from 'os';`, []byte(`{}`), 8*1024*1024, time.Second)
	if err == nil {
		t.Fatal("expected host module import to fail")
	}
}

func TestRunEnforcesMemoryLimit(t *testing.T) {
	_, err := Run(context.Background(), `(() => { const values = []; while (true) values.push('x'.repeat(1024)); })`, []byte(`{}`), 2*1024*1024, time.Second)
	if err == nil || !strings.Contains(err.Error(), "QuickJS") {
		t.Fatalf("expected bounded QuickJS failure, got %v", err)
	}
}

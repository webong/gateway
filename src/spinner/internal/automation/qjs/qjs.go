// Package qjs keeps Gateway's JSON-in/JSON-out interface to QuickJS-NG.
package qjs

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	quickjs "github.com/buke/quickjs-go"
)

func Run(ctx context.Context, source string, input []byte, memoryLimit uint64, timeout time.Duration) ([]byte, error) {
	if strings.IndexByte(source, 0) >= 0 || strings.IndexByte(string(input), 0) >= 0 {
		return nil, fmt.Errorf("QuickJS source and input cannot contain NUL bytes")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	if timeout <= 0 {
		return nil, context.DeadlineExceeded
	}
	if memoryLimit == 0 {
		return nil, fmt.Errorf("QuickJS memory limit is required")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	deadline := time.Now().Add(timeout)
	jsRuntime := quickjs.NewRuntime(
		quickjs.WithMemoryLimit(memoryLimit),
		quickjs.WithMaxStackSize(512*1024),
		quickjs.WithCanBlock(false),
		quickjs.WithModuleImport(false),
		quickjs.WithStrictOSThread(true),
	)
	if jsRuntime == nil {
		return nil, fmt.Errorf("cannot create QuickJS runtime")
	}
	defer jsRuntime.Close()
	jsRuntime.SetInterruptHandler(func() int {
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			return 1
		}
		return 0
	})

	jsContext := jsRuntime.NewBareContext()
	if jsContext == nil {
		return nil, fmt.Errorf("cannot create QuickJS context")
	}
	defer jsContext.Close()

	function := jsContext.Eval(source)
	if function == nil {
		return nil, fmt.Errorf("QuickJS execution failed")
	}
	defer function.Free()
	if function.IsException() {
		return nil, executionError(ctx, deadline, jsContext)
	}
	if !function.IsFunction() {
		return nil, fmt.Errorf("automation source is not callable")
	}

	argument := jsContext.ParseJSON(string(input))
	if argument == nil {
		return nil, fmt.Errorf("QuickJS input parsing failed")
	}
	defer argument.Free()
	if argument.IsException() {
		return nil, executionError(ctx, deadline, jsContext)
	}

	thisValue := jsContext.Undefined()
	defer thisValue.Free()
	result := function.Execute(thisValue, argument)
	if result == nil {
		return nil, fmt.Errorf("QuickJS execution failed")
	}
	defer result.Free()
	if result.IsException() {
		return nil, executionError(ctx, deadline, jsContext)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []byte(result.ToString()), nil
}

func executionError(ctx context.Context, deadline time.Time, jsContext *quickjs.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	if err := jsContext.Exception(); err != nil {
		return fmt.Errorf("QuickJS execution failed: %w", err)
	}
	return fmt.Errorf("QuickJS execution failed")
}

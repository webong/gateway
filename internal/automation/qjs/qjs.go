// Package qjs is Gateway's narrow bridge to a pinned QuickJS-NG engine.
// The only Go-facing operation exchanges JSON strings with an isolated VM.
package qjs

/*
#cgo CFLAGS: -D_GNU_SOURCE
#cgo linux LDFLAGS: -lm
#include <stdlib.h>
#include "bridge.h"
*/
import "C"

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unsafe"
)

func Run(ctx context.Context, source string, input []byte, memoryLimit uint64, timeout time.Duration) ([]byte, error) {
	if strings.IndexByte(source, 0) >= 0 || strings.IndexByte(string(input), 0) >= 0 {
		return nil, fmt.Errorf("QuickJS source and input cannot contain NUL bytes")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < timeout {
			timeout = remaining
		}
	}
	if timeout <= 0 {
		return nil, context.DeadlineExceeded
	}
	if memoryLimit == 0 {
		return nil, fmt.Errorf("QuickJS memory limit is required")
	}

	cSource := C.CString(source)
	defer C.free(unsafe.Pointer(cSource))
	cInput := C.CString(string(input))
	defer C.free(unsafe.Pointer(cInput))

	var output *C.char
	var failure *C.char
	status := C.gateway_qjs_run(cSource, cInput, C.size_t(memoryLimit), C.long(max(1, timeout.Milliseconds())), &output, &failure)
	if output != nil {
		defer C.free(unsafe.Pointer(output))
	}
	if failure != nil {
		defer C.free(unsafe.Pointer(failure))
	}
	if status != 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if failure == nil {
			return nil, fmt.Errorf("QuickJS execution failed")
		}
		return nil, fmt.Errorf("QuickJS execution failed: %s", C.GoString(failure))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []byte(C.GoString(output)), nil
}

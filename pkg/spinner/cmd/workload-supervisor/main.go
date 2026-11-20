// gateway-workload-supervisor owns a local workload independently of its controller.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/webong/workload"
)

func main() {
	directory := flag.String("state-dir", "", "private durable state directory")
	key := flag.String("key", "", "assignment identity hash")
	timeout := flag.Duration("stop-timeout", 5*time.Second, "total process shutdown budget")
	flag.Parse()
	if err := workload.ServeLocalSupervisor(*directory, *key, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

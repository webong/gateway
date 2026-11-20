//go:build !unix

package provision

import (
	"fmt"
	"os"
)

func lockWorker(*os.File) error {
	return fmt.Errorf("exclusive provisioning ownership is supported on Linux and macOS")
}

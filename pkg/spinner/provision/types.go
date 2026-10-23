package provision

import "github.com/webong/gateway/driver"

// These aliases preserve existing host integrations while extensions depend
// directly on the independently versioned driver module.
type ServerSpec = driver.ServerSpec
type LaunchSpec = driver.LaunchSpec
type Workload = driver.Workload
type InstanceReport = driver.InstanceReport
type Driver = driver.Driver
type Process = driver.Process

const (
	DesiredRunning   = driver.DesiredRunning
	DesiredStopped   = driver.DesiredStopped
	DriverLocal      = driver.DriverLocal
	DriverDocker     = driver.DriverDocker
	DriverKubernetes = driver.DriverKubernetes
)

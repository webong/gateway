//go:build !linux

package main

import "os/exec"

// Release targets are Linux; other platforms only run the contract/unit tests.
func configureChild(*exec.Cmd) {}

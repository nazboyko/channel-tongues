//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach puts the helper in its own session so it outlives the host process.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

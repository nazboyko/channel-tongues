//go:build windows

package main

import "os/exec"

// detach is a no-op on Windows; the helper keeps running after the host exits.
func detach(cmd *exec.Cmd) {}

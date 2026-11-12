//go:build !windows

package fusion

import "os/exec"

func hideCommand(command *exec.Cmd) {}

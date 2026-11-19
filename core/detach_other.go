//go:build !windows

package core

import (
	"os/exec"
	"syscall"
)

// detach makes cmd outlive this process (the room process is killed when the room closes).
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

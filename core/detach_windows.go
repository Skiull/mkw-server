package core

import (
	"os/exec"
	"syscall"
)

// detach makes cmd outlive this process (the room process is killed when the room closes)
// and run at below-normal priority, as do the Kinoko races it starts.
func detach(cmd *exec.Cmd) {
	const detachedProcess, newProcessGroup, belowNormal = 0x8, 0x200, 0x4000
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | newProcessGroup | belowNormal}
}

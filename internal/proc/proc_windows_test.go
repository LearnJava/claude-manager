//go:build windows

package proc

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestHideConsoleSetsFlags(t *testing.T) {
	cmd := exec.Command("cmd", "/C", "echo")
	HideConsole(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr not set")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Error("HideWindow = false, want true")
	}
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Error("CREATE_NO_WINDOW flag not set")
	}
}

func TestHideConsolePreservesExistingAttr(t *testing.T) {
	cmd := exec.Command("cmd", "/C", "echo")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200} // CREATE_NEW_PROCESS_GROUP
	HideConsole(cmd)
	if cmd.SysProcAttr.CreationFlags&0x00000200 == 0 {
		t.Error("pre-existing creation flag was clobbered")
	}
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Error("CREATE_NO_WINDOW flag not set")
	}
}

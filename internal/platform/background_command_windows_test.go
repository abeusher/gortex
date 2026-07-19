//go:build windows

package platform

import (
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestConfigureBackgroundCommandHidesWindowsConsole(t *testing.T) {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS | windows.CREATE_NEW_CONSOLE,
	}

	ConfigureBackgroundCommand(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("ConfigureBackgroundCommand left SysProcAttr nil")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Error("ConfigureBackgroundCommand left HideWindow disabled")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Error("ConfigureBackgroundCommand did not set CREATE_NO_WINDOW")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Error("ConfigureBackgroundCommand discarded existing creation flags")
	}
	if cmd.SysProcAttr.CreationFlags&windows.DETACHED_PROCESS != 0 {
		t.Error("ConfigureBackgroundCommand must not combine CREATE_NO_WINDOW with DETACHED_PROCESS")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NEW_CONSOLE != 0 {
		t.Error("ConfigureBackgroundCommand must not combine CREATE_NO_WINDOW with CREATE_NEW_CONSOLE")
	}
}

func TestConfigureBackgroundCommandNilIsSafe(t *testing.T) {
	ConfigureBackgroundCommand(nil)
}

func TestConfigureBackgroundCommandCreatesWindowsAttributes(t *testing.T) {
	cmd := exec.Command("cmd.exe")

	ConfigureBackgroundCommand(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("ConfigureBackgroundCommand left SysProcAttr nil")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Error("ConfigureBackgroundCommand left HideWindow disabled")
	}
	if cmd.SysProcAttr.CreationFlags != windows.CREATE_NO_WINDOW {
		t.Errorf("CreationFlags = %#x, want CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
}

package session

import "syscall"

// NewSysProcAttr returns platform-specific process attributes for the daemon.
func NewSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

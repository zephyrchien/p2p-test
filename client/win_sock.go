// +build win

package main

import (
	"golang.org/x/sys/windows"
	"syscall"
)

func CONTROL(network, address string, c syscall.RawConn) (err error) {
	return c.Control(func(fd uintptr) {
		err = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
	})
}

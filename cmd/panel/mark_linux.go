package main

import "syscall"

func routeMark(mark int) func(string, string, syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		var opt error
		err := c.Control(func(fd uintptr) { opt = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, mark) })
		if err != nil {
			return err
		}
		return opt
	}
}

//go:build !linux

package main

import (
	"errors"
	"syscall"
)

func routeMark(mark int) func(string, string, syscall.RawConn) error {
	return func(string, string, syscall.RawConn) error { return errors.New("Linux routing marks required") }
}

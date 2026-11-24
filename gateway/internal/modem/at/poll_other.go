//go:build !linux

package at

import "io"

func waitPortReadable(_ io.ReadWriteCloser, _ int) (ready bool, usedPoll bool) {
	return true, false
}

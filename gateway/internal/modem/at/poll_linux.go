//go:build linux

package at

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// waitPortReadable blocks in the kernel until the serial fd has data or a
// hangup/error event, or timeoutMs elapses. This eliminates the 100 Hz idle
// busy-poll timer while waking in sub-millisecond time for RING/+CMTI URCs.
func waitPortReadable(port io.ReadWriteCloser, timeoutMs int) (ready bool, usedPoll bool) {
	file, ok := port.(*os.File)
	if !ok {
		return true, false
	}
	fds := []unix.PollFd{{Fd: int32(file.Fd()), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, timeoutMs)
	if err != nil {
		if err == unix.EINTR {
			return false, true
		}
		return true, true
	}
	return n > 0, true
}

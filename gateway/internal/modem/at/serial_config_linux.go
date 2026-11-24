//go:build linux

package at

import (
	"context"
	"fmt"

	"golang.org/x/sys/unix"
)

// configureSerialNative configures termios directly on the already-opened fd
// on Linux, avoiding a redundant open/close (which can toggle DTR/HUPCL) and
// ensuring Cflag CBAUD matches Ispeed/Ospeed for TCSETS.
func configureSerialNative(ctx context.Context, fd int, path string, baud int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	termios, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return fmt.Errorf("read termios for serial port %s: %w", path, err)
	}
	termios.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	termios.Oflag &^= unix.OPOST
	termios.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	termios.Cflag &^= unix.CSIZE | unix.PARENB | unix.CBAUD
	speed := uint32(unix.B9600)
	if baud == 115200 {
		speed = uint32(unix.B115200)
	}
	termios.Cflag |= unix.CS8 | unix.CREAD | unix.CLOCAL | speed
	termios.Ispeed = speed
	termios.Ospeed = speed
	termios.Cc[unix.VMIN] = 1
	termios.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, termios); err != nil {
		return fmt.Errorf("write termios for serial port %s: %w", path, err)
	}
	return nil
}

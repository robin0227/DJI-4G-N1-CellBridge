//go:build !linux

package at

import (
	"context"
	"fmt"
)

func configureSerialNative(context.Context, int, string, int) error {
	return fmt.Errorf("native termios configuration is only used on Linux")
}

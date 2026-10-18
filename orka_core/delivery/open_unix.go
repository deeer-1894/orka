//go:build unix

package delivery

import (
	"os"
	"syscall"
)

// Nonblocking open prevents a concurrent replacement with a FIFO from hanging
// finalization. The caller verifies the opened descriptor is a regular file.
func openSource(root *os.Root, rel string) (*os.File, error) {
	return root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

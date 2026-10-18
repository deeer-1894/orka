//go:build unix

package artifacts

import (
	"os"
	"syscall"
)

// Nonblocking open prevents a concurrent FIFO replacement from hanging checks.
func openDeclared(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}

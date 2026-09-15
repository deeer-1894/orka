//go:build !unix

package artifacts

import "os"

func openDeclared(root *os.Root, name string) (*os.File, error) { return root.Open(name) }

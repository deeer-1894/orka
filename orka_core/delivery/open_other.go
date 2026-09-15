//go:build !unix

package delivery

import "os"

func openSource(root *os.Root, rel string) (*os.File, error) { return root.Open(rel) }

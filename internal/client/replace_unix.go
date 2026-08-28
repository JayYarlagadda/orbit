//go:build !windows

package client

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}

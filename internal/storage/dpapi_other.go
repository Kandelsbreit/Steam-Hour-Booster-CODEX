//go:build !windows

package storage

import (
	"errors"
	"os"
)

type DPAPI struct{}

func (DPAPI) Protect([]byte) ([]byte, error) {
	return nil, errors.New("DPAPI доступен только в Windows")
}
func (DPAPI) Unprotect([]byte) ([]byte, error) {
	return nil, errors.New("DPAPI доступен только в Windows")
}
func replaceFile(from, to string) error { return os.Rename(from, to) }

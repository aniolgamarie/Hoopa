//go:build !linux || !amd64

package coreadmission

import "errors"

func newBackend() (backend, error) {
	return nil, errors.New("core admission experiment requires linux/amd64")
}

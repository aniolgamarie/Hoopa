//go:build !linux || !amd64

package main

import (
	"errors"
	"io"
)

func runFixture(options, io.Writer) error {
	return errors.New("sourceidentity fixture requires linux/amd64")
}

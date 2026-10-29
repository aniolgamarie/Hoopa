//go:build sqlite_syncfault && sqlite_mattn && !sqlite_modernc && cgo && linux && amd64

package main

import (
	"errors"
	"github.com/mattn/go-sqlite3"
)

func sqliteSyncFaultExtendedCode(err error) int {
	var e sqlite3.Error
	if errors.As(err, &e) {
		return int(e.ExtendedCode)
	}
	return 0
}

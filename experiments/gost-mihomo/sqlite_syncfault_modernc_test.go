//go:build sqlite_syncfault && sqlite_modernc && !sqlite_mattn && linux && amd64

package main

import (
	"errors"
	"modernc.org/sqlite"
)

func sqliteSyncFaultExtendedCode(err error) int {
	var e *sqlite.Error
	if errors.As(err, &e) && e != nil {
		return e.Code()
	}
	return 0
}

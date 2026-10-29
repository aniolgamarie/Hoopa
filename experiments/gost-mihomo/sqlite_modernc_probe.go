//go:build sqlite_modernc && !sqlite_mattn

package main

import (
	"errors"

	"modernc.org/sqlite"
)

const sqliteProbeDriverName = "sqlite"

func sqliteProbeErrorCode(err error) int {
	var sqliteError *sqlite.Error
	if errors.As(err, &sqliteError) && sqliteError != nil {
		return sqliteError.Code() & 0xff
	}
	return 0
}

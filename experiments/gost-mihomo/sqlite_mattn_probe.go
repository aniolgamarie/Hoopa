//go:build sqlite_mattn && !sqlite_modernc && cgo

package main

import (
	"errors"

	"github.com/mattn/go-sqlite3"
)

const sqliteProbeDriverName = "sqlite3"

func sqliteProbeErrorCode(err error) int {
	var sqliteError sqlite3.Error
	if errors.As(err, &sqliteError) {
		return int(sqliteError.Code)
	}
	return 0
}

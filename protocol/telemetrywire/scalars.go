package telemetrywire

import (
	"errors"
	"regexp"
	"strconv"
	"time"
)

func parseID(s string) (string, error) {
	if len(s) == 0 || len(s) > 128 {
		return "", errors.New("invalid ID")
	}
	for i, c := range []byte(s) {
		alnum := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
		if !alnum && (i == 0 || c != '.' && c != '_' && c != ':' && c != '-') {
			return "", errors.New("invalid ID")
		}
	}
	return s, nil
}
func parseBytes(s string) (uint64, error) {
	if s == "" || len(s) > 1 && s[0] == '0' {
		return 0, errors.New("invalid decimal")
	}
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid decimal")
		}
	}
	return strconv.ParseUint(s, 10, 64)
}

var timestampForm = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,9})?Z$`)

func parseTimestamp(s string) (time.Time, error) {
	if !timestampForm.MatchString(s) {
		return time.Time{}, errors.New("invalid UTC timestamp")
	}
	return time.Parse(time.RFC3339Nano, s)
}

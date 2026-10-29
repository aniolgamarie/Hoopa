//go:build linux && amd64

package config

import (
	"context"
	"errors"
	"os"
)

const MaxAppLaunchReadBytes = 64 << 20

// ReadAppLaunchRecords returns a bounded complete inventory or an error. It
// takes only an existing shared allocation lock, never a wrapper owner lock.
func ReadAppLaunchRecords(ctx context.Context, paths Paths) (records []AppLaunchRecord, err error) {
	if ctx == nil {
		return nil, ErrAppLaunch
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	dir, history, err := openAppLaunchNamespace(paths)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, dir.Close())
		if err != nil {
			records = nil
		}
	}()
	lock, err := appLaunchLock(ctx, dir, false)
	if err != nil {
		return nil, err
	}
	ids, _, err := appLaunchInventory(ctx, dir)
	err = errors.Join(err, lock.Close())
	if err != nil {
		return nil, err
	}
	remaining := MaxAppLaunchReadBytes
	records = make([]AppLaunchRecord, 0, len(ids))
	for _, id := range ids {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		child, e := openPrivateDir(dir, string(id), false)
		if e != nil {
			return nil, e
		}
		record, e := readAppLaunchRecord(ctx, child, history, id, &remaining)
		e = errors.Join(e, child.Close())
		if e != nil {
			return nil, e
		}
		records = append(records, record)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func readAppLaunchRecord(ctx context.Context, dir *os.Root, history string, id ID, remaining *int) (out AppLaunchRecord, err error) {
	read := func(name string, optional bool) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, err := readBounded(dir, name, MaxAppLaunchRecordBytes)
		if optional && errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if len(raw) > *remaining {
			return nil, ErrAppLaunchCapacity
		}
		*remaining -= len(raw)
		return raw, nil
	}
	// Highest stage first: a concurrent later append may leave an earlier valid
	// prefix, but its already-published prerequisites cannot be missed afterward.
	terminalRaw, err := read("terminal.json", true)
	if err != nil {
		return out, err
	}
	authorizationRaw, err := read("authorization.json", true)
	if err != nil {
		return out, err
	}
	birthRaw, err := read("birth.json", true)
	if err != nil {
		return out, err
	}
	reservationRaw, err := read("reservation.json", false)
	if err != nil {
		return out, err
	}
	if decode(reservationRaw, &out.Reservation) != nil || out.Reservation.Validate() != nil || out.Reservation.HistoryID != history || out.Reservation.LaunchID != id {
		return AppLaunchRecord{}, ErrAppLaunch
	}
	out.ReservationSHA256 = digest(reservationRaw)
	if birthRaw != nil {
		b := AppLaunchBirth{}
		if decode(birthRaw, &b) != nil || b.Match(out.Reservation, out.ReservationSHA256) != nil {
			return AppLaunchRecord{}, ErrAppLaunch
		}
		sha := digest(birthRaw)
		out.Birth, out.BirthSHA256 = &b, &sha
	}
	if authorizationRaw != nil {
		a := AppLaunchAuthorization{}
		if out.Birth == nil || decode(authorizationRaw, &a) != nil || a.Match(out.Reservation, out.ReservationSHA256, *out.Birth, *out.BirthSHA256) != nil {
			return AppLaunchRecord{}, ErrAppLaunch
		}
		sha := digest(authorizationRaw)
		out.Authorization, out.AuthorizationSHA256 = &a, &sha
	}
	if terminalRaw != nil {
		t := AppLaunchTerminal{}
		if out.Birth == nil || decode(terminalRaw, &t) != nil || t.Match(out.Reservation, out.ReservationSHA256, *out.Birth, *out.BirthSHA256, out.Authorization, out.AuthorizationSHA256) != nil {
			return AppLaunchRecord{}, ErrAppLaunch
		}
		sha := digest(terminalRaw)
		out.Terminal, out.TerminalSHA256 = &t, &sha
	}
	entries, err := boundedEntries(dir, 16)
	if err != nil {
		return AppLaunchRecord{}, err
	}
	ownerSeen := false
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return AppLaunchRecord{}, err
		}
		name := entry.Name()
		switch name {
		case ".owner.lock":
			if _, err := readBounded(dir, name, 0); err != nil {
				return AppLaunchRecord{}, err
			}
			ownerSeen = true
		case "reservation.json", "birth.json", "authorization.json", "terminal.json":
			// A later stage may have appeared after the highest-first read. Do
			// not mix it into that earlier prefix. Published originals never move.
			if err := checkPrivateInputFile(dir, name); err != nil {
				return AppLaunchRecord{}, err
			}
		default:
			if !appLaunchTemporary(name) {
				return AppLaunchRecord{}, ErrAppLaunch
			}
			f, err := openPrivateFile(dir, name, os.O_RDONLY)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return AppLaunchRecord{}, err
			}
			info, e := f.Stat()
			e = errors.Join(e, f.Close())
			if e != nil {
				return AppLaunchRecord{}, e
			}
			if info.Size() < 0 || info.Size() > MaxAppLaunchRecordBytes {
				return AppLaunchRecord{}, ErrAppLaunch
			}
		}
	}
	if !ownerSeen {
		return AppLaunchRecord{}, ErrAppLaunch
	}
	return out, nil
}

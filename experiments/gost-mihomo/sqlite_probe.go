//go:build sqlite_modernc || sqlite_mattn

package main

// This is a temporary T019 transaction probe, not the production ledger.
// Driver registration is selected at build time; there is no runtime fallback.
import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

var (
	errSQLiteProbeConflict        = errors.New("probe sample identity conflict")
	errSQLiteProbeIntegrity       = errors.New("probe counter or sequence integrity error")
	errSQLiteProbeUncertain       = errors.New("probe commit response lost; reopen and reconcile")
	errSQLiteProbeReadUnavailable = errors.New("probe cannot open without possible recovery or sidecar creation")
)

const sqliteProbeSchema = `
CREATE TABLE probe_meta (singleton INTEGER PRIMARY KEY CHECK(singleton=1), version INTEGER NOT NULL CHECK(version>=0));
INSERT INTO probe_meta VALUES (1,0);
CREATE TABLE probe_samples (
 series TEXT NOT NULL, epoch TEXT NOT NULL, sample_id TEXT NOT NULL, payload_hash TEXT NOT NULL,
 PRIMARY KEY(series,epoch,sample_id));
CREATE TABLE probe_checkpoints (
 series TEXT NOT NULL, epoch TEXT NOT NULL, sequence INTEGER NOT NULL CHECK(sequence>0),
 upload INTEGER NOT NULL CHECK(typeof(upload)='integer' AND upload>=0),
 download INTEGER NOT NULL CHECK(typeof(download)='integer' AND download>=0),
 PRIMARY KEY(series,epoch));
CREATE TABLE probe_facts (
 series TEXT NOT NULL, epoch TEXT NOT NULL, sample_id TEXT NOT NULL,
 upload_delta INTEGER NOT NULL CHECK(typeof(upload_delta)='integer' AND upload_delta>=0),
 download_delta INTEGER NOT NULL CHECK(typeof(download_delta)='integer' AND download_delta>=0),
 PRIMARY KEY(series,epoch,sample_id),
 FOREIGN KEY(series,epoch,sample_id) REFERENCES probe_samples(series,epoch,sample_id));
CREATE TABLE probe_gaps (
 series TEXT NOT NULL, epoch TEXT NOT NULL, sample_id TEXT NOT NULL, reason TEXT NOT NULL,
 PRIMARY KEY(series,epoch,sample_id),
 FOREIGN KEY(series,epoch,sample_id) REFERENCES probe_samples(series,epoch,sample_id));
`

type sqliteProbeSample struct {
	Series, Epoch, ID          string
	Sequence, Upload, Download int64
	GapReason                  string
}

type sqliteProbe struct {
	db        *sql.DB
	conn      *sql.Conn
	uncertain bool
}

// readOnly never creates a schema, changes journaling, checkpoints, or repairs.
// SQLite mode=ro alone is not a promise that opening WAL creates no sidecars.
// The tests measure that separately; this function is not a product read API.
func openSQLiteProbe(ctx context.Context, path string, readOnly bool) (*sqliteProbe, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if readOnly {
		if err = sqliteProbeReadPreflight(abs); err != nil {
			return nil, err
		}
	}
	q := url.Values{}
	if readOnly {
		q.Set("mode", "ro")
	} else {
		q.Set("mode", "rwc")
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: q.Encode()}
	db, err := sql.Open(sqliteProbeDriverName, u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	c, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}
	p := &sqliteProbe{db: db, conn: c}
	if err = c.PingContext(ctx); err != nil {
		p.close()
		return nil, err
	}
	if !readOnly {
		if err = p.configureWriter(ctx); err != nil {
			p.close()
			return nil, err
		}
	}
	return p, nil
}

// This conservative check is for a controlled fixture with stable paths. It is
// not a race-safe production path/owner validation or a recovery mechanism.
func sqliteProbeReadPreflight(path string) error {
	regular := func(p string) error {
		s, err := os.Lstat(p)
		if err != nil || !s.Mode().IsRegular() {
			return errSQLiteProbeReadUnavailable
		}
		return nil
	}
	if err := regular(path); err != nil {
		return err
	}
	if _, err := os.Lstat(path + "-journal"); !errors.Is(err, os.ErrNotExist) {
		return errSQLiteProbeReadUnavailable
	}
	f, err := os.Open(path)
	if err != nil {
		return errSQLiteProbeReadUnavailable
	}
	var header [20]byte
	_, err = f.ReadAt(header[:], 0)
	closeErr := f.Close()
	if err != nil || closeErr != nil || string(header[:16]) != "SQLite format 3\x00" {
		return errSQLiteProbeReadUnavailable
	}
	// Existing WAL/SHM alone is insufficient: a mode=ro connection may update
	// shared-memory read marks. This direct-file fallback conservatively refuses
	// WAL, including a closed WAL database. Do not switch journal modes or use
	// immutable=1 to bypass coordination with a live writer during a query.
	if header[18] != 1 || header[19] != 1 {
		return errSQLiteProbeReadUnavailable
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			return errSQLiteProbeReadUnavailable
		}
	}
	return nil
}

func (p *sqliteProbe) close() error {
	return errors.Join(p.conn.Close(), p.db.Close())
}

func (p *sqliteProbe) initialize(ctx context.Context) error {
	var mode string
	if err := p.conn.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return err
	}
	if mode != "wal" {
		return fmt.Errorf("unexpected journal mode %q", mode)
	}
	_, err := p.conn.ExecContext(ctx, sqliteProbeSchema)
	return err
}

func (p *sqliteProbe) configureWriter(ctx context.Context) error {
	for _, statement := range []string{"PRAGMA synchronous=FULL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=50", "PRAGMA wal_autocheckpoint=0"} {
		if _, err := p.conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	for pragma, want := range map[string]int{"synchronous": 2, "foreign_keys": 1, "busy_timeout": 50, "wal_autocheckpoint": 0} {
		var got int
		if err := p.conn.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("PRAGMA %s: %d, want %d", pragma, got, want)
		}
	}
	return nil
}

// hook is only an experiment seam. after_commit can hide an actual successful
// commit; that models lost application confirmation, not a SQLite fsync fault.
// Any real Commit error also returns uncertain and must be reconciled after close.
func (p *sqliteProbe) apply(ctx context.Context, s sqliteProbeSample, hook func(string) error) (replayed bool, result error) {
	if p.uncertain {
		return false, errSQLiteProbeUncertain
	}
	if s.Series == "" || s.Epoch == "" || s.ID == "" || s.Sequence <= 0 || s.Upload < 0 || s.Download < 0 {
		return false, errSQLiteProbeIntegrity
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return false, err
	}
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	tx, err := p.conn.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() {
		if e := tx.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
			p.uncertain = true
			replayed = false
			result = errors.Join(result, fmt.Errorf("%w: rollback: %v", errSQLiteProbeUncertain, e))
		}
	}()
	var priorHash string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash FROM probe_samples WHERE series=? AND epoch=? AND sample_id=?`, s.Series, s.Epoch, s.ID).Scan(&priorHash)
	if err == nil {
		if priorHash != digest {
			return false, errSQLiteProbeConflict
		}
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var seq, up, down int64
	err = tx.QueryRowContext(ctx, `SELECT sequence,upload,download FROM probe_checkpoints WHERE series=? AND epoch=?`, s.Series, s.Epoch).Scan(&seq, &up, &down)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if s.Sequence <= seq || s.Upload < up || s.Download < down {
		return false, errSQLiteProbeIntegrity
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO probe_samples VALUES (?,?,?,?)`, s.Series, s.Epoch, s.ID, digest); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO probe_facts VALUES (?,?,?,?,?)`, s.Series, s.Epoch, s.ID, s.Upload-up, s.Download-down); err != nil {
		return false, err
	}
	if hook != nil {
		if err = hook("after_fact"); err != nil {
			return false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO probe_checkpoints VALUES (?,?,?,?,?) ON CONFLICT(series,epoch) DO UPDATE SET sequence=excluded.sequence,upload=excluded.upload,download=excluded.download`, s.Series, s.Epoch, s.Sequence, s.Upload, s.Download); err != nil {
		return false, err
	}
	if s.GapReason != "" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO probe_gaps VALUES (?,?,?,?)`, s.Series, s.Epoch, s.ID, s.GapReason); err != nil {
			return false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE probe_meta SET version=version+1 WHERE singleton=1`); err != nil {
		return false, err
	}
	if hook != nil {
		if err = hook("before_commit"); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		p.uncertain = true
		return false, fmt.Errorf("%w: %w", errSQLiteProbeUncertain, err)
	}
	if hook != nil {
		if err = hook("after_commit"); err != nil {
			p.uncertain = true
			return false, fmt.Errorf("%w: %w", errSQLiteProbeUncertain, err)
		}
	}
	return false, nil
}

type sqliteProbeState struct {
	Samples, Facts, Checkpoints, Gaps, Version int64
	Upload, Download, Sequence                 int64
	CheckpointUpload, CheckpointDownload       int64
}

// The controlled probe has one writer. Inspect after it has stopped or while it
// is held at a barrier; production snapshot/concurrency handling is out of scope.
func (p *sqliteProbe) state(ctx context.Context) (sqliteProbeState, error) {
	var s sqliteProbeState
	err := p.conn.QueryRowContext(ctx, `SELECT
 (SELECT COUNT(*) FROM probe_samples), (SELECT COUNT(*) FROM probe_facts),
 (SELECT COUNT(*) FROM probe_checkpoints), (SELECT COUNT(*) FROM probe_gaps),
 (SELECT version FROM probe_meta WHERE singleton=1),
 (SELECT COALESCE(SUM(upload_delta),0) FROM probe_facts),
 (SELECT COALESCE(SUM(download_delta),0) FROM probe_facts),
 (SELECT COALESCE(MAX(sequence),0) FROM probe_checkpoints),
 (SELECT COALESCE(SUM(upload),0) FROM probe_checkpoints),
 (SELECT COALESCE(SUM(download),0) FROM probe_checkpoints)`).Scan(&s.Samples, &s.Facts, &s.Checkpoints, &s.Gaps, &s.Version, &s.Upload, &s.Download, &s.Sequence, &s.CheckpointUpload, &s.CheckpointDownload)
	return s, err
}

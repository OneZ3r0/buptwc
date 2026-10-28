package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/OneZ3r0/buptwc/internal/schedule"
	"github.com/OneZ3r0/buptwc/internal/state"
	_ "modernc.org/sqlite"
)

var ErrNoCourses = errors.New("尚未导入课表，请运行 buptwc import <file.xls>")

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if err := state.EnsureDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("数据库必须为普通文件")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	err = f.Chmod(0600)
	f.Close()
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) init() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version == 1 {
		return nil
	}
	if version > 1 {
		return fmt.Errorf("数据库版本 %d 高于当前程序支持版本", version)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS timetable (id INTEGER PRIMARY KEY CHECK(id=1), courses TEXT NOT NULL, imported_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS accounts (account_id TEXT PRIMARY KEY, last_scan INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS seen (account_id TEXT NOT NULL, notification_id TEXT NOT NULL, seen_at INTEGER NOT NULL, PRIMARY KEY(account_id, notification_id));
CREATE TABLE IF NOT EXISTS outbox (
 id INTEGER PRIMARY KEY, account_id TEXT NOT NULL, notification_id TEXT NOT NULL,
 message TEXT NOT NULL, expires_at INTEGER NOT NULL, next_attempt INTEGER NOT NULL,
 attempts INTEGER NOT NULL DEFAULT 0, state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','sent','expired','failed')),
 last_error TEXT NOT NULL DEFAULT '', UNIQUE(account_id, notification_id),
 FOREIGN KEY(account_id, notification_id) REFERENCES seen(account_id, notification_id)
);
CREATE INDEX IF NOT EXISTS outbox_due ON outbox(account_id, state, next_attempt);
PRAGMA user_version=1;`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReplaceCourses(ctx context.Context, courses []schedule.Course, now time.Time) error {
	if len(courses) == 0 {
		return fmt.Errorf("拒绝写入空课表")
	}
	b, err := json.Marshal(courses)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO timetable(id,courses,imported_at) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET courses=excluded.courses,imported_at=excluded.imported_at`, string(b), now.UnixMilli())
	return err
}

func (s *Store) Courses(ctx context.Context) ([]schedule.Course, error) {
	var data string
	if err := s.db.QueryRowContext(ctx, "SELECT courses FROM timetable WHERE id=1").Scan(&data); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNoCourses
		}
		return nil, err
	}
	var courses []schedule.Course
	err := json.Unmarshal([]byte(data), &courses)
	return courses, err
}

type Observed struct {
	ID, Message string
	ExpiresAt   time.Time
}
type ScanResult struct {
	Baseline    bool
	New, Queued int
}

// ApplyScan atomically establishes a baseline or records new IDs and their jobs.
// Callers must only supply a successfully completed scan.
func (s *Store) ApplyScan(ctx context.Context, account string, records []Observed, now time.Time) (ScanResult, error) {
	var result ScanResult
	if account == "" {
		return result, fmt.Errorf("account_id 不能为空")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var last int64
	err = tx.QueryRowContext(ctx, "SELECT last_scan FROM accounts WHERE account_id=?", account).Scan(&last)
	if err != nil && err != sql.ErrNoRows {
		return result, err
	}
	result.Baseline = err == sql.ErrNoRows
	for _, n := range records {
		if n.ID == "" {
			return ScanResult{}, fmt.Errorf("通知 ID 不能为空")
		}
		r, err := tx.ExecContext(ctx, "INSERT INTO seen(account_id,notification_id,seen_at) VALUES(?,?,?) ON CONFLICT DO NOTHING", account, n.ID, now.UnixMilli())
		if err != nil {
			return ScanResult{}, err
		}
		inserted, err := r.RowsAffected()
		if err != nil {
			return ScanResult{}, err
		}
		if inserted == 0 {
			continue
		}
		result.New++
		if result.Baseline || n.Message == "" || !n.ExpiresAt.After(now) {
			continue
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO outbox(account_id,notification_id,message,expires_at,next_attempt) VALUES(?,?,?,?,?)", account, n.ID, n.Message, n.ExpiresAt.UnixMilli(), now.UnixMilli())
		if err != nil {
			return ScanResult{}, err
		}
		result.Queued++
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO accounts(account_id,last_scan) VALUES(?,?) ON CONFLICT(account_id) DO UPDATE SET last_scan=excluded.last_scan", account, now.UnixMilli())
	if err != nil {
		return ScanResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ScanResult{}, err
	}
	return result, nil
}

type Job struct {
	ID        int64
	Message   string
	ExpiresAt time.Time
	Attempts  int
}

func (s *Store) Due(ctx context.Context, account string, now time.Time, limit int) ([]Job, error) {
	if limit < 1 {
		return nil, fmt.Errorf("任务批量上限必须为正")
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE outbox SET state='expired' WHERE account_id=? AND state='pending' AND expires_at<=?", account, now.UnixMilli()); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,message,expires_at,attempts FROM outbox WHERE account_id=? AND state='pending' AND next_attempt<=? ORDER BY next_attempt,id LIMIT ?", account, now.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		var j Job
		var expiry int64
		if err := rows.Scan(&j.ID, &j.Message, &expiry, &j.Attempts); err != nil {
			return nil, err
		}
		j.ExpiresAt = time.UnixMilli(expiry)
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (s *Store) Finish(ctx context.Context, id int64, state string, attempts int, next time.Time, lastError string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE outbox SET state=?,attempts=?,next_attempt=?,last_error=? WHERE id=? AND state='pending'", state, attempts, next.UnixMilli(), lastError, id)
	return err
}

func (s *Store) NextJob(ctx context.Context, account string) (time.Time, bool, error) {
	var when sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT MIN(MIN(next_attempt,expires_at)) FROM outbox WHERE account_id=? AND state='pending'", account).Scan(&when)
	if err != nil || !when.Valid {
		return time.Time{}, false, err
	}
	return time.UnixMilli(when.Int64), true, nil
}

type Status struct {
	Baseline                             bool
	LastScan                             time.Time
	Seen, Pending, Sent, Failed, Expired int
}

func (s *Store) Status(ctx context.Context, account string) (Status, error) {
	var st Status
	var last int64
	err := s.db.QueryRowContext(ctx, "SELECT last_scan FROM accounts WHERE account_id=?", account).Scan(&last)
	if err != nil && err != sql.ErrNoRows {
		return st, err
	}
	if err == nil {
		st.Baseline = true
		st.LastScan = time.UnixMilli(last)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM seen WHERE account_id=?", account).Scan(&st.Seen); err != nil {
		return st, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT state,COUNT(*) FROM outbox WHERE account_id=? GROUP BY state", account)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return st, err
		}
		switch state {
		case "pending":
			st.Pending = count
		case "sent":
			st.Sent = count
		case "failed":
			st.Failed = count
		case "expired":
			st.Expired = count
		}
	}
	return st, rows.Err()
}

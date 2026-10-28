package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestScanPersistenceAndIsolation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Now().Truncate(time.Millisecond)
	old := Observed{ID: "old", Message: "历史", ExpiresAt: now.Add(time.Hour)}
	fresh := Observed{ID: "new", Message: "新提醒", ExpiresAt: now.Add(time.Hour)}
	r, err := s.ApplyScan(ctx, "A", []Observed{old}, now)
	if err != nil || !r.Baseline || r.New != 1 || r.Queued != 0 {
		t.Fatalf("baseline: %+v, %v", r, err)
	}
	r, err = s.ApplyScan(ctx, "A", []Observed{old, fresh, {ID: "expired", Message: "过期", ExpiresAt: now}, {ID: "other"}}, now)
	if err != nil || r.Baseline || r.New != 3 || r.Queued != 1 {
		t.Fatalf("incremental: %+v, %v", r, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	fresh.Message = "更新同 ID 内容"
	r, err = s.ApplyScan(ctx, "A", []Observed{fresh, old}, now)
	if err != nil || r.Baseline || r.New != 0 || r.Queued != 0 {
		t.Fatalf("restart dedup: %+v, %v", r, err)
	}
	jobs, err := s.Due(ctx, "A", now, 10)
	if err != nil || len(jobs) != 1 || jobs[0].Message != "新提醒" {
		t.Fatalf("recovered jobs: %+v, %v", jobs, err)
	}
	other, err := s.Due(ctx, "B", now, 10)
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-account jobs: %+v, %v", other, err)
	}
	r, err = s.ApplyScan(ctx, "B", []Observed{fresh}, now)
	if err != nil || !r.Baseline || r.New != 1 || r.Queued != 0 {
		t.Fatalf("other baseline: %+v, %v", r, err)
	}
	if err := s.Finish(ctx, jobs[0].ID, "pending", 1, now.Add(time.Minute), "retry"); err != nil {
		t.Fatal(err)
	}
	at, ok, err := s.NextJob(ctx, "A")
	if err != nil || !ok || !at.Equal(now.Add(time.Minute)) {
		t.Fatalf("retry wake: %v %t %v", at, ok, err)
	}
	jobs, err = s.Due(ctx, "A", now.Add(time.Hour), 10)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("expired dispatched: %v, %v", jobs, err)
	}
	st, err := s.Status(ctx, "A")
	if err != nil || st.Expired != 1 || st.Seen != 4 || st.Pending != 0 {
		t.Fatalf("status: %+v, %v", st, err)
	}
}

func TestScanRollback(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	for _, baseline := range []bool{false, true} {
		if baseline {
			if _, err := s.ApplyScan(ctx, "A", nil, now); err != nil {
				t.Fatal(err)
			}
		}
		_, err = s.ApplyScan(ctx, "A", []Observed{{ID: "new", Message: "提醒", ExpiresAt: now.Add(time.Hour)}, {ID: ""}}, now)
		if err == nil {
			t.Fatal("invalid scan succeeded")
		}
		st, err := s.Status(ctx, "A")
		if err != nil || st.Baseline != baseline || st.Seen != 0 || st.Pending != 0 {
			t.Fatalf("partial scan committed: %+v, %v", st, err)
		}
	}
}

func TestOutboxFailureRollsBackSeen(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	if _, err := s.ApplyScan(ctx, "A", nil, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_outbox BEFORE INSERT ON outbox BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	records := []Observed{{ID: "new", Message: "提醒", ExpiresAt: now.Add(time.Hour)}}
	if _, err := s.ApplyScan(ctx, "A", records, now); err == nil {
		t.Fatal("outbox failure ignored")
	}
	st, err := s.Status(ctx, "A")
	if err != nil || st.Seen != 0 || st.Pending != 0 {
		t.Fatalf("seen committed without outbox: %+v %v", st, err)
	}
	if _, err := s.db.Exec("DROP TRIGGER fail_outbox"); err != nil {
		t.Fatal(err)
	}
	r, err := s.ApplyScan(ctx, "A", records, now)
	if err != nil || r.Queued != 1 {
		t.Fatalf("retry lost notification: %+v %v", r, err)
	}
}

func TestOpenExistingWhileWriterActive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO accounts VALUES('A',1)"); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err = reader.Status(context.Background(), "A"); err != nil {
		t.Fatal(err)
	}
}

func TestDueLimitDoesNotConsumeUnselectedJobs(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Now()
	if _, err := s.ApplyScan(ctx, "A", nil, now); err != nil {
		t.Fatal(err)
	}
	records := []Observed{{ID: "1", Message: "first", ExpiresAt: now.Add(time.Hour)}, {ID: "2", Message: "second", ExpiresAt: now.Add(time.Hour)}}
	if _, err := s.ApplyScan(ctx, "A", records, now); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.Due(ctx, "A", now, 1)
	if err != nil || len(jobs) != 1 || jobs[0].Message != "first" {
		t.Fatalf("%v %v", jobs, err)
	}
	if err := s.Finish(ctx, jobs[0].ID, "sent", 1, time.Time{}, ""); err != nil {
		t.Fatal(err)
	}
	jobs, err = s.Due(ctx, "A", now, 1)
	if err != nil || len(jobs) != 1 || jobs[0].Message != "second" {
		t.Fatalf("%v %v", jobs, err)
	}
	if _, err := s.Due(ctx, "A", now, 0); err == nil {
		t.Fatal("unbounded batch accepted")
	}
}

package cron

import (
	"testing"

	"github.com/0xJacky/Nginx-UI/internal/testdb"
	"github.com/0xJacky/Nginx-UI/model"
	"github.com/0xJacky/Nginx-UI/query"
	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// useCronTestDB points the model and query packages at a fresh in-memory
// SQLite database migrated with the given models.
func useCronTestDB(t *testing.T, models ...any) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(testdb.DSN(t)), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(models...))

	model.Use(db)
	query.Use(db)
	query.SetDefault(db)

	return db
}

// newTestScheduler returns a scheduler that is never started, so the jobs
// registered on it are tracked but never executed.
func newTestScheduler(t *testing.T, options ...gocron.SchedulerOption) gocron.Scheduler {
	t.Helper()

	scheduler, err := gocron.NewScheduler(options...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = scheduler.Shutdown() })

	return scheduler
}

// jobNames returns the names of the jobs registered on the scheduler.
func jobNames(scheduler gocron.Scheduler) []string {
	jobs := scheduler.Jobs()
	names := make([]string, 0, len(jobs))
	for _, job := range jobs {
		names = append(names, job.Name())
	}
	return names
}

// hasJob reports whether a job with the given ID is registered on the scheduler.
func hasJob(scheduler gocron.Scheduler, id uuid.UUID) bool {
	for _, job := range scheduler.Jobs() {
		if job.ID() == id {
			return true
		}
	}
	return false
}

// countJobsNamed counts the jobs with the given name on the scheduler.
func countJobsNamed(scheduler gocron.Scheduler, name string) int {
	count := 0
	for _, n := range jobNames(scheduler) {
		if n == name {
			count++
		}
	}
	return count
}

// isolateAutoBackupJobs gives the test an empty auto backup job registry and
// removes whatever the test registered on the global scheduler afterwards.
func isolateAutoBackupJobs(t *testing.T) {
	t.Helper()

	autoBackupMu.Lock()
	saved := autoBackupJobs
	autoBackupJobs = make(map[uint64]gocron.Job)
	autoBackupMu.Unlock()

	t.Cleanup(func() {
		// A deadlocked test can leave the lock held forever; fail fast
		// instead of hanging the whole test binary on cleanup.
		if !autoBackupMu.TryLock() {
			t.Error("autoBackupMu is still held; auto backup registry not restored")
			return
		}
		defer autoBackupMu.Unlock()
		for _, job := range autoBackupJobs {
			_ = s.RemoveJob(job.ID())
		}
		autoBackupJobs = saved
	})
}

// isolateDDNSJobs gives the test an empty DDNS job registry and removes
// whatever the test registered on the global scheduler afterwards.
func isolateDDNSJobs(t *testing.T) {
	t.Helper()

	ddnsMu.Lock()
	saved := ddnsJobs
	ddnsJobs = make(map[uint64]gocron.Job)
	ddnsMu.Unlock()

	t.Cleanup(func() {
		ddnsMu.Lock()
		defer ddnsMu.Unlock()
		for _, job := range ddnsJobs {
			_ = s.RemoveJob(job.ID())
		}
		ddnsJobs = saved
	})
}

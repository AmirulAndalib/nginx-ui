package cron

import (
	"testing"
	"time"

	"github.com/0xJacky/Nginx-UI/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useAutoBackupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return useCronTestDB(t, &model.AutoBackup{}, &model.Notification{}, &model.ExternalNotify{})
}

// createAutoBackup stores an auto backup task. Enabled defaults to true in the
// schema, so a disabled task is switched off after it is created.
func createAutoBackup(t *testing.T, db *gorm.DB, name, cronExpression string, enabled bool) *model.AutoBackup {
	t.Helper()

	task := &model.AutoBackup{
		Name:           name,
		BackupType:     model.BackupTypeNginxAndNginxUI,
		StorageType:    model.StorageTypeLocal,
		StoragePath:    t.TempDir(),
		CronExpression: cronExpression,
		Enabled:        true,
	}
	require.NoError(t, db.Create(task).Error)
	if !enabled {
		require.NoError(t, db.Model(task).Update("enabled", false).Error)
		task.Enabled = false
	}
	return task
}

func TestAddAutoBackupJobRegistersNamedCronJob(t *testing.T) {
	isolateAutoBackupJobs(t)
	scheduler := newTestScheduler(t)

	require.NoError(t, addAutoBackupJob(scheduler, 7, "0 3 * * *"))

	job, ok := autoBackupJobs[7]
	require.True(t, ok, "job must be tracked by its backup ID")
	assert.Equal(t, "auto_backup_7", job.Name())
	assert.Equal(t, []string{"auto_backup_7"}, jobNames(scheduler))
}

func TestAddAutoBackupJobRejectsInvalidCronExpression(t *testing.T) {
	isolateAutoBackupJobs(t)
	scheduler := newTestScheduler(t)

	for _, expression := range []string{"", "not a cron", "61 * * * *", "* * * * * *"} {
		t.Run(expression, func(t *testing.T) {
			err := addAutoBackupJob(scheduler, 1, expression)
			require.Error(t, err)
			assert.NotContains(t, autoBackupJobs, uint64(1))
			assert.Empty(t, scheduler.Jobs())
		})
	}
}

func TestAddAutoBackupJobReplacesExistingJob(t *testing.T) {
	isolateAutoBackupJobs(t)
	scheduler := newTestScheduler(t)

	require.NoError(t, addAutoBackupJob(scheduler, 3, "0 1 * * *"))
	first := autoBackupJobs[3]

	require.NoError(t, addAutoBackupJob(scheduler, 3, "30 2 * * *"))
	second := autoBackupJobs[3]

	assert.NotEqual(t, first.ID(), second.ID())
	assert.False(t, hasJob(scheduler, first.ID()), "replaced job must be removed from the scheduler")
	assert.True(t, hasJob(scheduler, second.ID()))
	assert.Len(t, scheduler.Jobs(), 1)
}

func TestAddAutoBackupJobKeepsOtherBackupsWhenReplacementFails(t *testing.T) {
	isolateAutoBackupJobs(t)
	scheduler := newTestScheduler(t)

	require.NoError(t, addAutoBackupJob(scheduler, 1, "0 1 * * *"))
	require.NoError(t, addAutoBackupJob(scheduler, 2, "0 2 * * *"))

	// An invalid expression drops the old job of that backup but leaves the
	// other backups untouched.
	require.Error(t, addAutoBackupJob(scheduler, 1, "bogus"))

	assert.NotContains(t, autoBackupJobs, uint64(1))
	assert.Contains(t, autoBackupJobs, uint64(2))
	assert.Equal(t, []string{"auto_backup_2"}, jobNames(scheduler))
}

func TestRemoveAutoBackupJob(t *testing.T) {
	isolateAutoBackupJobs(t)
	scheduler := newTestScheduler(t)

	require.NoError(t, addAutoBackupJob(scheduler, 5, "0 5 * * *"))
	job := autoBackupJobs[5]

	require.NoError(t, removeAutoBackupJob(scheduler, 5))
	assert.NotContains(t, autoBackupJobs, uint64(5))
	assert.False(t, hasJob(scheduler, job.ID()))

	// Removing an unknown backup is a no-op.
	require.NoError(t, removeAutoBackupJob(scheduler, 5))
	require.NoError(t, removeAutoBackupJob(scheduler, 999))
}

func TestAutoBackupPublicAPIUsesGlobalScheduler(t *testing.T) {
	isolateAutoBackupJobs(t)

	require.NoError(t, AddAutoBackupJob(11, "0 4 * * *"))
	added := autoBackupJobs[11]
	assert.True(t, hasJob(s, added.ID()))

	require.NoError(t, UpdateAutoBackupJob(11, "15 4 * * *"))
	updated := autoBackupJobs[11]
	assert.NotEqual(t, added.ID(), updated.ID())
	assert.False(t, hasJob(s, added.ID()))
	assert.True(t, hasJob(s, updated.ID()))

	require.Error(t, UpdateAutoBackupJob(11, "invalid"))

	require.NoError(t, AddAutoBackupJob(12, "0 6 * * *"))
	job := autoBackupJobs[12]
	require.NoError(t, RemoveAutoBackupJob(12))
	assert.False(t, hasJob(s, job.ID()))
	assert.NotContains(t, autoBackupJobs, uint64(12))
}

func TestSetupAutoBackupJobsSchedulesEnabledTasksOnly(t *testing.T) {
	db := useAutoBackupTestDB(t)
	isolateAutoBackupJobs(t)
	scheduler := newTestScheduler(t)

	enabled := createAutoBackup(t, db, "enabled", "0 1 * * *", true)
	disabled := createAutoBackup(t, db, "disabled", "0 2 * * *", false)
	// A task with a broken expression is logged and skipped without failing
	// the whole setup.
	broken := createAutoBackup(t, db, "broken", "nope", true)

	require.NoError(t, setupAutoBackupJobs(scheduler))

	assert.Contains(t, autoBackupJobs, enabled.ID)
	assert.NotContains(t, autoBackupJobs, disabled.ID)
	assert.NotContains(t, autoBackupJobs, broken.ID)
	assert.Len(t, scheduler.Jobs(), 1)
}

func TestSetupAutoBackupJobsReportsDatabaseErrors(t *testing.T) {
	// The auto backup table is missing, so loading the tasks fails.
	useCronTestDB(t, &model.Notification{})
	isolateAutoBackupJobs(t)
	scheduler := newTestScheduler(t)

	require.Error(t, setupAutoBackupJobs(scheduler))
	assert.Empty(t, scheduler.Jobs())
}

func TestRestartAutoBackupJobsReaddsEnabledTasks(t *testing.T) {
	db := useAutoBackupTestDB(t)
	isolateAutoBackupJobs(t)

	enabled := createAutoBackup(t, db, "nightly", "0 3 * * *", true)
	require.NoError(t, AddAutoBackupJob(enabled.ID, enabled.CronExpression))
	previous := autoBackupJobs[enabled.ID]

	done := make(chan error, 1)
	go func() { done <- RestartAutoBackupJobs() }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		// The stuck call keeps autoBackupMu locked, which would hang every
		// later test in the package, so abort the binary instead of t.Fatal.
		panic("RestartAutoBackupJobs deadlocked with an enabled task")
	}

	require.Contains(t, autoBackupJobs, enabled.ID)
	assert.NotEqual(t, previous.ID(), autoBackupJobs[enabled.ID].ID())
	assert.False(t, hasJob(s, previous.ID()))
	assert.True(t, hasJob(s, autoBackupJobs[enabled.ID].ID()))

	// The registry lock must be free again after the restart.
	require.NoError(t, RemoveAutoBackupJob(enabled.ID))
}

func TestRestartAutoBackupJobsDropsJobsOfMissingAndDisabledTasks(t *testing.T) {
	db := useAutoBackupTestDB(t)
	isolateAutoBackupJobs(t)

	// A job for a task that no longer exists must be dropped on restart.
	require.NoError(t, AddAutoBackupJob(9999, "0 0 * * *"))
	stale := autoBackupJobs[9999]

	disabled := createAutoBackup(t, db, "paused", "0 4 * * *", false)
	require.NoError(t, AddAutoBackupJob(disabled.ID, disabled.CronExpression))
	disabledJob := autoBackupJobs[disabled.ID]

	require.NoError(t, RestartAutoBackupJobs())

	assert.False(t, hasJob(s, stale.ID()))
	assert.False(t, hasJob(s, disabledJob.ID()))
	assert.Empty(t, autoBackupJobs)
}

func TestRestartAutoBackupJobsReportsDatabaseErrors(t *testing.T) {
	useCronTestDB(t, &model.Notification{})
	isolateAutoBackupJobs(t)

	require.NoError(t, AddAutoBackupJob(42, "0 0 * * *"))
	stale := autoBackupJobs[42]

	require.Error(t, RestartAutoBackupJobs())
	assert.False(t, hasJob(s, stale.ID()), "existing jobs are dropped before reloading")
	assert.Empty(t, autoBackupJobs)
}

func TestExecuteAutoBackupTaskRemovesJobOfDisabledTask(t *testing.T) {
	db := useAutoBackupTestDB(t)
	isolateAutoBackupJobs(t)

	task := createAutoBackup(t, db, "paused", "0 1 * * *", false)
	require.NoError(t, AddAutoBackupJob(task.ID, task.CronExpression))
	job := autoBackupJobs[task.ID]

	executeAutoBackupTask(task.ID)

	assert.NotContains(t, autoBackupJobs, task.ID)
	assert.False(t, hasJob(s, job.ID()))

	var stored model.AutoBackup
	require.NoError(t, db.First(&stored, task.ID).Error)
	assert.Empty(t, stored.LastBackupError, "a disabled task must not run")
}

func TestExecuteAutoBackupTaskIgnoresMissingTask(t *testing.T) {
	db := useAutoBackupTestDB(t)
	isolateAutoBackupJobs(t)

	require.NoError(t, AddAutoBackupJob(404, "0 1 * * *"))
	job := autoBackupJobs[404]

	executeAutoBackupTask(404)

	// The job stays registered; only a disabled task unregisters itself.
	assert.True(t, hasJob(s, job.ID()))

	var notifications int64
	require.NoError(t, db.Model(&model.Notification{}).Count(&notifications).Error)
	assert.Zero(t, notifications)
}

func TestExecuteAutoBackupTaskRecordsFailureOfEnabledTask(t *testing.T) {
	db := useAutoBackupTestDB(t)
	isolateAutoBackupJobs(t)

	task := createAutoBackup(t, db, "misconfigured", "0 1 * * *", true)
	// An unsupported storage type fails validation before any file is touched.
	require.NoError(t, db.Model(task).Update("storage_type", "ftp").Error)

	executeAutoBackupTask(task.ID)

	var stored model.AutoBackup
	require.NoError(t, db.First(&stored, task.ID).Error)
	assert.Equal(t, model.BackupStatusFailed, stored.LastBackupStatus)
	assert.NotEmpty(t, stored.LastBackupError)

	var notifications []model.Notification
	require.NoError(t, db.Find(&notifications).Error)
	require.Len(t, notifications, 1)
	assert.Equal(t, model.NotificationError, notifications[0].Type)
	assert.Equal(t, "Auto Backup Configuration Error", notifications[0].Title)
}

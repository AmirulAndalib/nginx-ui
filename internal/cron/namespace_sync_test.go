package cron

import (
	"testing"
	"time"

	"github.com/0xJacky/Nginx-UI/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// isolateNamespaceSyncState gives the test an empty record of past runs.
func isolateNamespaceSyncState(t *testing.T) {
	t.Helper()
	reset := func() {
		lastNamespaceSync.Range(func(key, _ any) bool {
			lastNamespaceSync.Delete(key)
			return true
		})
	}
	reset()
	t.Cleanup(reset)
}

func lastNamespaceSyncAt(t *testing.T, id uint64) (time.Time, bool) {
	t.Helper()
	value, ok := lastNamespaceSync.Load(id)
	if !ok {
		return time.Time{}, false
	}
	return value.(time.Time), true
}

func createNamespace(t *testing.T, db *gorm.DB, namespace *model.Namespace) *model.Namespace {
	t.Helper()
	require.NoError(t, db.Create(namespace).Error)
	return namespace
}

func TestSetupNamespaceSyncJobRegistersNamedJob(t *testing.T) {
	scheduler := newTestScheduler(t)

	job, err := setupNamespaceSyncJob(scheduler)
	require.NoError(t, err)

	assert.Equal(t, "namespace_auto_sync", job.Name())
	assert.True(t, hasJob(scheduler, job.ID()))
}

func TestExecuteNamespaceAutoSyncOnlyRunsDueAutoNamespacesWithNodes(t *testing.T) {
	db := useCronTestDB(t, &model.Namespace{}, &model.Node{})
	isolateNamespaceSyncState(t)

	manual := createNamespace(t, db, &model.Namespace{
		Name:         "manual",
		SyncStrategy: model.SyncStrategyManual,
		SyncNodeIds:  []uint64{1},
	})
	withoutNodes := createNamespace(t, db, &model.Namespace{
		Name:         "no-nodes",
		SyncStrategy: model.SyncStrategyAuto,
	})
	// The node does not exist, so the replication itself fails, which only
	// gets logged; the run is still recorded so it is not retried every tick.
	auto := createNamespace(t, db, &model.Namespace{
		Name:         "auto",
		SyncStrategy: model.SyncStrategyAuto,
		SyncNodeIds:  []uint64{404},
	})

	before := time.Now()
	executeNamespaceAutoSync()

	_, ok := lastNamespaceSyncAt(t, manual.ID)
	assert.False(t, ok, "manual namespaces are never auto synced")
	_, ok = lastNamespaceSyncAt(t, withoutNodes.ID)
	assert.False(t, ok, "namespaces without nodes are skipped")

	ranAt, ok := lastNamespaceSyncAt(t, auto.ID)
	require.True(t, ok, "auto namespace with nodes must be synced")
	assert.False(t, ranAt.Before(before))
}

func TestExecuteNamespaceAutoSyncHonoursInterval(t *testing.T) {
	db := useCronTestDB(t, &model.Namespace{}, &model.Node{})
	isolateNamespaceSyncState(t)

	notDue := createNamespace(t, db, &model.Namespace{
		Name:                "not-due",
		SyncStrategy:        model.SyncStrategyAuto,
		SyncNodeIds:         []uint64{1},
		SyncIntervalMinutes: 30,
	})
	due := createNamespace(t, db, &model.Namespace{
		Name:                "due",
		SyncStrategy:        model.SyncStrategyAuto,
		SyncNodeIds:         []uint64{1},
		SyncIntervalMinutes: 5,
	})
	// SyncIntervalMinutes is stored as the schema default (30) when zero, so
	// a negative value exercises the "use the default" fallback.
	defaultInterval := createNamespace(t, db, &model.Namespace{
		Name:                "default-interval",
		SyncStrategy:        model.SyncStrategyAuto,
		SyncNodeIds:         []uint64{1},
		SyncIntervalMinutes: -1,
	})

	now := time.Now()
	notDueAt := now.Add(-10 * time.Minute)
	dueAt := now.Add(-10 * time.Minute)
	defaultNotDueAt := now.Add(-time.Duration(model.DefaultSyncIntervalMinutes-1) * time.Minute)
	lastNamespaceSync.Store(notDue.ID, notDueAt)
	lastNamespaceSync.Store(due.ID, dueAt)
	lastNamespaceSync.Store(defaultInterval.ID, defaultNotDueAt)

	executeNamespaceAutoSync()

	got, _ := lastNamespaceSyncAt(t, notDue.ID)
	assert.Equal(t, notDueAt, got, "namespace inside its interval must not run")

	got, _ = lastNamespaceSyncAt(t, due.ID)
	assert.True(t, got.After(dueAt), "namespace past its interval must run")

	got, _ = lastNamespaceSyncAt(t, defaultInterval.ID)
	assert.Equal(t, defaultNotDueAt, got, "default interval must apply when none is set")
}

func TestExecuteNamespaceAutoSyncStopsOnDatabaseError(t *testing.T) {
	// The namespace table is missing, so listing namespaces fails.
	useCronTestDB(t, &model.Node{})
	isolateNamespaceSyncState(t)

	executeNamespaceAutoSync()

	count := 0
	lastNamespaceSync.Range(func(_, _ any) bool {
		count++
		return true
	})
	assert.Zero(t, count)
}

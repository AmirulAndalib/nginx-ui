package cron

import (
	"testing"

	"github.com/0xJacky/Nginx-UI/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddDDNSJobRejectsNonPositiveInterval(t *testing.T) {
	isolateDDNSJobs(t)
	scheduler := newTestScheduler(t)

	for _, interval := range []int{0, -1, -300} {
		err := addDDNSJob(scheduler, 1, interval)
		require.Error(t, err, "interval %d", interval)
	}

	assert.Empty(t, ddnsJobs)
	assert.Empty(t, scheduler.Jobs())
}

func TestAddDDNSJobRegistersAndReplacesJob(t *testing.T) {
	isolateDDNSJobs(t)
	scheduler := newTestScheduler(t)

	require.NoError(t, addDDNSJob(scheduler, 8, 300))
	first := ddnsJobs[8]
	require.NotNil(t, first)
	assert.Equal(t, "ddns_8", first.Name())

	require.NoError(t, addDDNSJob(scheduler, 8, 600))
	second := ddnsJobs[8]

	assert.NotEqual(t, first.ID(), second.ID())
	assert.False(t, hasJob(scheduler, first.ID()), "replaced job must be removed")
	assert.Equal(t, []string{"ddns_8"}, jobNames(scheduler))
}

func TestAddDDNSJobKeepsExistingJobWhenIntervalInvalid(t *testing.T) {
	isolateDDNSJobs(t)
	scheduler := newTestScheduler(t)

	require.NoError(t, addDDNSJob(scheduler, 2, 120))
	job := ddnsJobs[2]

	// The interval is validated before the old job is touched.
	require.Error(t, addDDNSJob(scheduler, 2, 0))

	assert.Equal(t, job.ID(), ddnsJobs[2].ID())
	assert.True(t, hasJob(scheduler, job.ID()))
}

func TestRemoveDDNSJob(t *testing.T) {
	isolateDDNSJobs(t)
	scheduler := newTestScheduler(t)

	require.NoError(t, addDDNSJob(scheduler, 4, 60))
	require.NoError(t, addDDNSJob(scheduler, 5, 60))
	removed := ddnsJobs[4]

	require.NoError(t, removeDDNSJob(scheduler, 4))

	assert.NotContains(t, ddnsJobs, uint64(4))
	assert.False(t, hasJob(scheduler, removed.ID()))
	assert.Equal(t, []string{"ddns_5"}, jobNames(scheduler))

	// Removing an unknown domain is a no-op.
	require.NoError(t, removeDDNSJob(scheduler, 4))
}

func TestDDNSPublicAPIUsesGlobalScheduler(t *testing.T) {
	isolateDDNSJobs(t)

	require.NoError(t, AddOrUpdateDDNSJob(21, 90))
	first := ddnsJobs[21]
	assert.True(t, hasJob(s, first.ID()))

	require.NoError(t, AddOrUpdateDDNSJob(21, 180))
	second := ddnsJobs[21]
	assert.False(t, hasJob(s, first.ID()))
	assert.True(t, hasJob(s, second.ID()))

	require.Error(t, AddOrUpdateDDNSJob(22, 0))
	assert.NotContains(t, ddnsJobs, uint64(22))

	require.NoError(t, RemoveDDNSJob(21))
	assert.False(t, hasJob(s, second.ID()))
	assert.NotContains(t, ddnsJobs, uint64(21))
}

func TestSetupDDNSJobsSchedulesEnabledDomainsWithTargets(t *testing.T) {
	db := useCronTestDB(t, &model.DnsCredential{}, &model.DnsDomain{})
	isolateDDNSJobs(t)
	scheduler := newTestScheduler(t)

	target := []model.DDNSRecordTarget{{}}
	domains := []*model.DnsDomain{
		{Domain: "enabled.example.com", DDNSConfig: &model.DDNSConfig{Enabled: true, IntervalSeconds: 600, Targets: target}},
		{Domain: "disabled.example.com", DDNSConfig: &model.DDNSConfig{Enabled: false, IntervalSeconds: 600, Targets: target}},
		{Domain: "no-targets.example.com", DDNSConfig: &model.DDNSConfig{Enabled: true, IntervalSeconds: 600}},
		{Domain: "unconfigured.example.com"},
		// A too short interval is clamped by the DNS package, not rejected.
		{Domain: "short.example.com", DDNSConfig: &model.DDNSConfig{Enabled: true, IntervalSeconds: 1, Targets: target}},
	}
	for i, domain := range domains {
		domain.DnsCredentialID = uint64(i + 1)
		require.NoError(t, db.Create(domain).Error)
	}

	require.NoError(t, setupDDNSJobs(scheduler))

	assert.Contains(t, ddnsJobs, domains[0].ID)
	assert.NotContains(t, ddnsJobs, domains[1].ID)
	assert.NotContains(t, ddnsJobs, domains[2].ID)
	assert.NotContains(t, ddnsJobs, domains[3].ID)
	assert.Contains(t, ddnsJobs, domains[4].ID)
	assert.Len(t, scheduler.Jobs(), 2)
}

func TestSetupDDNSJobsReportsDatabaseErrors(t *testing.T) {
	// The DNS domain table is missing, so loading the schedules fails.
	useCronTestDB(t, &model.DnsCredential{})
	isolateDDNSJobs(t)
	scheduler := newTestScheduler(t)

	require.Error(t, setupDDNSJobs(scheduler))
	assert.Empty(t, scheduler.Jobs())
}

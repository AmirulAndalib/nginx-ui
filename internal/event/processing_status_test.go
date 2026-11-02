package event

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetProcessingStatus clears every flag on the singleton manager before and
// after the test, so tests do not depend on each other's leftovers.
func resetProcessingStatus(t *testing.T) *ProcessingStatusManager {
	t.Helper()
	m := GetProcessingStatusManager()
	reset := func() {
		m.UpdateIndexScanning(false)
		m.UpdateAutoCertProcessing(false)
		m.UpdateNginxLogIndexing(false)
	}
	reset()
	t.Cleanup(reset)
	return m
}

func TestGetProcessingStatusManagerReturnsSingleton(t *testing.T) {
	assert.Same(t, GetProcessingStatusManager(), GetProcessingStatusManager())
}

func TestProcessingStatusUpdatesPublishFullStatus(t *testing.T) {
	tests := []struct {
		name   string
		update func(m *ProcessingStatusManager, value bool)
		want   ProcessingStatusData
	}{
		{
			name:   "index scanning",
			update: (*ProcessingStatusManager).UpdateIndexScanning,
			want:   ProcessingStatusData{IndexScanning: true},
		},
		{
			name:   "auto cert processing",
			update: (*ProcessingStatusManager).UpdateAutoCertProcessing,
			want:   ProcessingStatusData{AutoCertProcessing: true},
		},
		{
			name:   "nginx log indexing",
			update: (*ProcessingStatusManager).UpdateNginxLogIndexing,
			want:   ProcessingStatusData{NginxLogIndexing: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := resetProcessingStatus(t)
			hub := installGlobalHub(t)

			tt.update(m, true)

			assert.Equal(t, tt.want, m.GetCurrentStatus())
			messages := hub.snapshot()
			require.NotEmpty(t, messages)
			assert.Equal(t, recordedMessage{event: string(TypeProcessingStatus), data: tt.want}, messages[0])
		})
	}
}

func TestProcessingStatusUnchangedValueDoesNotPublish(t *testing.T) {
	tests := []struct {
		name   string
		update func(m *ProcessingStatusManager, value bool)
	}{
		{name: "index scanning", update: (*ProcessingStatusManager).UpdateIndexScanning},
		{name: "auto cert processing", update: (*ProcessingStatusManager).UpdateAutoCertProcessing},
		{name: "nginx log indexing", update: (*ProcessingStatusManager).UpdateNginxLogIndexing},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := resetProcessingStatus(t)
			tt.update(m, true)

			hub := installGlobalHub(t)
			tt.update(m, true)
			assert.Empty(t, hub.snapshot(), "re-applying the current value should not publish")
		})
	}

	t.Run("false while already false", func(t *testing.T) {
		m := resetProcessingStatus(t)
		hub := installGlobalHub(t)

		m.UpdateIndexScanning(false)
		m.UpdateAutoCertProcessing(false)
		m.UpdateNginxLogIndexing(false)

		assert.Empty(t, hub.snapshot())
	})
}

func TestProcessingStatusFlagsAreIndependent(t *testing.T) {
	m := resetProcessingStatus(t)
	hub := installGlobalHub(t)

	m.UpdateIndexScanning(true)
	m.UpdateAutoCertProcessing(true)
	m.UpdateIndexScanning(false)

	assert.Equal(t, ProcessingStatusData{AutoCertProcessing: true}, m.GetCurrentStatus())
	assert.Equal(t, []recordedMessage{
		{event: string(TypeProcessingStatus), data: ProcessingStatusData{IndexScanning: true}},
		{event: string(TypeProcessingStatus), data: ProcessingStatusData{IndexScanning: true, AutoCertProcessing: true}},
		{event: string(TypeProcessingStatus), data: ProcessingStatusData{AutoCertProcessing: true}},
	}, hub.snapshot())
}

func TestUpdateNginxLogIndexingAlsoPublishesLegacyStatus(t *testing.T) {
	m := resetProcessingStatus(t)
	hub := installGlobalHub(t)

	m.UpdateNginxLogIndexing(true)
	m.UpdateNginxLogIndexing(false)

	assert.Equal(t, []recordedMessage{
		{event: string(TypeProcessingStatus), data: ProcessingStatusData{NginxLogIndexing: true}},
		{event: string(TypeNginxLogStatus), data: NginxLogStatusData{Indexing: true}},
		{event: string(TypeProcessingStatus), data: ProcessingStatusData{}},
		{event: string(TypeNginxLogStatus), data: NginxLogStatusData{Indexing: false}},
	}, hub.snapshot())
}

func TestBroadcastCurrentStatusAlwaysPublishes(t *testing.T) {
	m := resetProcessingStatus(t)
	m.UpdateAutoCertProcessing(true)
	hub := installGlobalHub(t)

	m.BroadcastCurrentStatus()
	m.BroadcastCurrentStatus()

	want := recordedMessage{event: string(TypeProcessingStatus), data: ProcessingStatusData{AutoCertProcessing: true}}
	assert.Equal(t, []recordedMessage{want, want}, hub.snapshot())
}

func TestProcessingStatusConcurrentUpdates(t *testing.T) {
	m := resetProcessingStatus(t)
	installGlobalHub(t)

	const iterations = 100
	updates := []func(bool){m.UpdateIndexScanning, m.UpdateAutoCertProcessing, m.UpdateNginxLogIndexing}

	var wg sync.WaitGroup
	for _, update := range updates {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range iterations {
				update(i%2 == 0)
				_ = m.GetCurrentStatus()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range iterations {
			m.BroadcastCurrentStatus()
		}
	}()
	wg.Wait()

	// The last iteration of each goroutine writes false (iterations is even).
	assert.Equal(t, ProcessingStatusData{}, m.GetCurrentStatus())
}

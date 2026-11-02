package event

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetWebSocketHub restores the package-level hub manager to its
// uninitialized state when the test ends.
func resetWebSocketHub(t *testing.T) {
	t.Helper()
	ShutdownWebSocketHub()
	wsHubManager = nil
	t.Cleanup(func() {
		ShutdownWebSocketHub()
		wsHubManager = nil
	})
}

func TestGetWebSocketContextBeforeInitReturnsBackground(t *testing.T) {
	resetWebSocketHub(t)

	assert.Equal(t, context.Background(), GetWebSocketContext())
}

func TestShutdownWebSocketHubBeforeInitIsNoop(t *testing.T) {
	resetWebSocketHub(t)

	assert.NotPanics(t, ShutdownWebSocketHub)
}

func TestWebSocketHubShutdownCancelsContext(t *testing.T) {
	resetWebSocketHub(t)

	InitWebSocketHub(context.Background())
	ctx := GetWebSocketContext()
	require.NoError(t, ctx.Err())

	ShutdownWebSocketHub()

	select {
	case <-ctx.Done():
	default:
		t.Fatal("WebSocket context should be cancelled after ShutdownWebSocketHub")
	}
}

func TestWebSocketHubFollowsParentContext(t *testing.T) {
	resetWebSocketHub(t)

	parent, cancel := context.WithCancel(context.Background())
	InitWebSocketHub(parent)
	ctx := GetWebSocketContext()
	require.NoError(t, ctx.Err())

	cancel()

	select {
	case <-ctx.Done():
	default:
		t.Fatal("WebSocket context should be cancelled when its parent is cancelled")
	}
}

func TestInitWebSocketHubReplacesContext(t *testing.T) {
	resetWebSocketHub(t)

	// Re-initializing does not cancel the previous context, so tie both to a
	// parent that the test cancels to release the watcher goroutines.
	parent, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	InitWebSocketHub(parent)
	first := GetWebSocketContext()
	InitWebSocketHub(parent)
	second := GetWebSocketContext()

	assert.NotSame(t, first, second)

	ShutdownWebSocketHub()
	assert.Error(t, second.Err(), "shutdown should cancel the current context")
}

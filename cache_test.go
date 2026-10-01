package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheFallback(t *testing.T) {
	tests := []struct {
		name          string
		seed          bool
		fresh         bool
		providerFails bool
		wantError     bool
		wantStale     bool
		calls         int
	}{
		{"fresh cache", true, true, false, false, false, 0},
		{"refresh expired", true, false, false, false, false, 1},
		{"cached fallback", true, false, true, true, true, 1},
		{"no cache on failure", false, false, true, true, false, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache, err := newCache(t.TempDir())
			require.NoError(t, err)
			if tt.seed {
				require.NoError(t, cache.put("point", map[string]string{"value": "old"}))
				if !tt.fresh {
					e := cache.entries["point"]
					e.FetchedAt = time.Now().Add(-2 * time.Hour).Unix()
					cache.entries["point"] = e
				}
			}
			calls := 0
			e, stale, err := cache.load(context.Background(), "point", time.Hour, func(context.Context) (any, error) {
				calls++
				if tt.providerFails {
					return nil, errors.New("offline")
				}
				return map[string]string{"value": "new"}, nil
			})
			if tt.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.NotEmpty(t, e.Data)
			}
			assert.Equal(t, tt.wantStale, stale)
			assert.Equal(t, tt.calls, calls)
		})
	}
}
func TestCacheSharedRefresh(t *testing.T) {
	cache, err := newCache(t.TempDir())
	require.NoError(t, err)
	var calls atomic.Int32
	var group sync.WaitGroup
	start := make(chan struct{})
	for range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			entry, stale, err := cache.load(context.Background(), "shared", time.Hour, func(context.Context) (any, error) {
				calls.Add(1)
				time.Sleep(20 * time.Millisecond)
				return "forecast", nil
			})
			assert.NoError(t, err)
			assert.False(t, stale)
			assert.JSONEq(t, `"forecast"`, string(entry.Data))
		}()
	}
	close(start)
	group.Wait()
	assert.Equal(t, int32(1), calls.Load())
}
func TestCachePersists(t *testing.T) {
	dir := t.TempDir()
	cache, err := newCache(dir)
	require.NoError(t, err)
	require.NoError(t, cache.put("point", []string{"forecast"}))
	reloaded, err := newCache(dir)
	require.NoError(t, err)
	got, ok := reloaded.lookup("point")
	require.True(t, ok)
	assert.JSONEq(t, `["forecast"]`, string(got.Data))
}

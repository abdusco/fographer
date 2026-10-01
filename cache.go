package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Entry struct {
	Data      json.RawMessage `json:"data"`
	FetchedAt int64           `json:"fetchedAt"`
}
type Envelope struct {
	Entry
	Source  string            `json:"source"`
	Stale   bool              `json:"stale"`
	Offline bool              `json:"offline,omitempty"`
	Warning string            `json:"warning,omitempty"`
	Units   map[string]string `json:"units,omitempty"`
}
type Cache struct {
	mu      sync.Mutex
	entries map[string]Entry
	pending map[string]chan struct{}
	dir     string
}

func newCache(dir string) (*Cache, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	c := &Cache{entries: map[string]Entry{}, pending: map[string]chan struct{}{}, dir: dir}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var record struct {
			Key   string `json:"key"`
			Entry Entry  `json:"entry"`
		}
		if json.Unmarshal(b, &record) == nil && record.Key != "" && json.Valid(record.Entry.Data) && time.Now().Unix()-record.Entry.FetchedAt < 86400 {
			c.entries[record.Key] = record.Entry
		}
	}
	return c, nil
}
func (c *Cache) lookup(key string) (Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	return e, ok
}
func (c *Cache) put(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	e := Entry{b, time.Now().Unix()}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Bound long-running personal-server memory and disk use.
	if len(c.entries) >= 512 {
		var oldest string
		age := int64(1 << 62)
		for k, v := range c.entries {
			if k != "overview" && k != "observations" && v.FetchedAt < age {
				oldest = k
				age = v.FetchedAt
			}
		}
		if oldest != "" {
			delete(c.entries, oldest)
			_ = os.Remove(c.filename(oldest))
		}
	}
	c.entries[key] = e
	record, err := json.Marshal(struct {
		Key   string `json:"key"`
		Entry Entry  `json:"entry"`
	}{key, e})
	if err != nil {
		return err
	}
	return atomicWrite(c.filename(key), record)
}
func (c *Cache) filename(key string) string {
	return filepath.Join(c.dir, fmt.Sprintf("%x.json", sha256.Sum256([]byte(key))))
}
func atomicWrite(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".snapshot-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func (c *Cache) load(ctx context.Context, key string, ttl time.Duration, fetch func(context.Context) (any, error)) (Entry, bool, error) {
	for {
		c.mu.Lock()
		e, ok := c.entries[key]
		if ok && time.Since(time.Unix(e.FetchedAt, 0)) < ttl {
			c.mu.Unlock()
			return e, false, nil
		}
		if ch, loading := c.pending[key]; loading {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return e, ok, ctx.Err()
			case <-ch:
				continue
			}
		}
		ch := make(chan struct{})
		c.pending[key] = ch
		c.mu.Unlock()
		v, err := fetch(ctx)
		if err == nil {
			err = c.put(key, v)
		}
		c.mu.Lock()
		delete(c.pending, key)
		close(ch)
		c.mu.Unlock()
		if err != nil {
			return e, ok, err
		}
		e, _ = c.lookup(key)
		return e, false, nil
	}
}

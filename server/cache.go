package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type diskCache struct {
	dir      string
	maxBytes int64
	total    atomic.Int64
	locks    [64]sync.Mutex
}

type cacheMeta struct {
	StatusCode  int         `json:"statusCode"`
	Headers     http.Header `json:"headers"`
	Size        int64       `json:"size"`
	StoredAt    time.Time   `json:"storedAt"`
	ExpiresAt   time.Time   `json:"expiresAt"`
	LastAccess  time.Time   `json:"lastAccess"`
}

func (m *cacheMeta) header() map[string]string {
	out := map[string]string{}
	for _, k := range []string{"Content-Type", "Content-Disposition", "ETag", "Last-Modified"} {
		if v := m.Headers.Get(k); v != "" {
			out[k] = v
		}
	}
	return out
}

func newDiskCache(dir string, maxBytes int64) (*diskCache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	c := &diskCache{dir: dir, maxBytes: maxBytes}
	var total int64
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".body") {
			if info, ierr := d.Info(); ierr == nil {
				total += info.Size()
			}
		}
		return nil
	})
	c.total.Store(total)
	return c, nil
}

func (c *diskCache) size() int64 { return c.total.Load() }

func (c *diskCache) paths(key string) (body, meta string) {
	sub := filepath.Join(c.dir, key[:2])
	return filepath.Join(sub, key+".body"), filepath.Join(sub, key+".json")
}

func (c *diskCache) lockFor(key string) *sync.Mutex {
	var h byte
	for i := 0; i < len(key); i++ {
		h ^= key[i]
	}
	return &c.locks[h%64]
}

// get 返回的 ReadCloser 由调用方关闭。
func (c *diskCache) get(key string) (*cacheMeta, io.ReadCloser, bool) {
	mu := c.lockFor(key)
	mu.Lock()
	defer mu.Unlock()

	bodyPath, metaPath := c.paths(key)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, nil, false
	}
	var m cacheMeta
	if json.Unmarshal(data, &m) != nil {
		return nil, nil, false
	}
	if time.Now().After(m.ExpiresAt) {
		c.removeLocked(key)
		return nil, nil, false
	}
	f, err := os.Open(bodyPath)
	if err != nil {
		c.removeLocked(key)
		return nil, nil, false
	}
	m.LastAccess = time.Now()
	if buf, err := json.Marshal(&m); err == nil {
		os.WriteFile(metaPath, buf, 0o644)
	}
	return &m, f, true
}

type cacheWriter struct {
	c        *diskCache
	key      string
	meta     cacheMeta
	tmpPath  string
	f        *os.File
	written  int64
	done     bool
}

func (c *diskCache) beginWrite(key string, header http.Header, ttl time.Duration) *cacheWriter {
	mu := c.lockFor(key)
	mu.Lock()
	defer mu.Unlock()

	bodyPath, _ := c.paths(key)
	if err := os.MkdirAll(filepath.Dir(bodyPath), 0o755); err != nil {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(bodyPath), "tmp-*")
	if err != nil {
		return nil
	}
	now := time.Now()
	return &cacheWriter{
		c:       c,
		key:     key,
		tmpPath: tmp.Name(),
		f:       tmp,
		meta: cacheMeta{
			StatusCode: http.StatusOK,
			Headers:    header.Clone(),
			StoredAt:   now,
			ExpiresAt:  now.Add(ttl),
			LastAccess: now,
		},
	}
}

func (cw *cacheWriter) Write(b []byte) (int, error) {
	n, err := cw.f.Write(b)
	cw.written += int64(n)
	return n, err
}

func (cw *cacheWriter) commit() {
	if cw.done {
		return
	}
	cw.done = true
	cw.f.Close()

	mu := cw.c.lockFor(cw.key)
	mu.Lock()
	defer mu.Unlock()

	bodyPath, metaPath := cw.c.paths(cw.key)
	cw.meta.Size = cw.written
	if old, err := os.Stat(bodyPath); err == nil {
		cw.c.total.Add(-old.Size())
		os.Remove(bodyPath)
	}
	if err := os.Rename(cw.tmpPath, bodyPath); err != nil {
		os.Remove(cw.tmpPath)
		return
	}
	buf, err := json.Marshal(&cw.meta)
	if err != nil {
		os.Remove(bodyPath)
		return
	}
	if err := os.WriteFile(metaPath, buf, 0o644); err != nil {
		os.Remove(bodyPath)
		return
	}
	cw.c.total.Add(cw.written)
	cw.c.evictLocked()
}

func (cw *cacheWriter) abort() {
	if cw.done {
		return
	}
	cw.done = true
	cw.f.Close()
	os.Remove(cw.tmpPath)
}

func (c *diskCache) removeLocked(key string) {
	bodyPath, metaPath := c.paths(key)
	if info, err := os.Stat(bodyPath); err == nil {
		c.total.Add(-info.Size())
	}
	os.Remove(bodyPath)
	os.Remove(metaPath)
}

// evictLocked 在超出容量时按最久未访问删除，调用方需持有任意一把锁（扫描是全局的，并发删除幂等）。
func (c *diskCache) evictLocked() {
	if c.maxBytes <= 0 || c.total.Load() <= c.maxBytes {
		return
	}
	type item struct {
		key  string
		atime time.Time
		size int64
	}
	var items []item
	filepath.WalkDir(c.dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		var m cacheMeta
		if json.Unmarshal(data, &m) != nil {
			return nil
		}
		key := strings.TrimSuffix(filepath.Base(p), ".json")
		items = append(items, item{key: key, atime: m.LastAccess, size: m.Size})
		return nil
	})
	sort.Slice(items, func(i, j int) bool { return items[i].atime.Before(items[j].atime) })
	for _, it := range items {
		if c.total.Load() <= c.maxBytes {
			break
		}
		c.removeLocked(it.key)
	}
}
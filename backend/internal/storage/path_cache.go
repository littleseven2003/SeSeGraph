package storage

import (
	"sync"
	"time"
)

// pathEntry 是缓存的远端文件路径，路径只在提供方内部使用。
type pathEntry struct {
	path    string
	size    int64
	expires time.Time
}

// pathCache 是带过期时间与容量上限的路径缓存。
// 容量达到上限时先清理过期条目，仍不足则淘汰最早过期的一条。
type pathCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	maxSize int
	entries map[string]pathEntry
}

// newPathCache 创建缓存，参数非正时使用默认值。
func newPathCache(ttl time.Duration, maxSize int) *pathCache {
	if ttl <= 0 {
		ttl = DefaultPathTTL
	}
	if maxSize <= 0 {
		maxSize = DefaultPathCacheSize
	}
	return &pathCache{
		ttl:     ttl,
		maxSize: maxSize,
		entries: make(map[string]pathEntry, maxSize),
	}
}

// get 返回未过期的缓存项，过期条目顺手删除。
func (c *pathCache) get(fileID string, now time.Time) (pathEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[fileID]
	if !ok {
		return pathEntry{}, false
	}
	if !now.Before(entry.expires) {
		delete(c.entries, fileID)
		return pathEntry{}, false
	}
	return entry, true
}

// set 写入缓存，未指定过期时间时按 TTL 计算。
func (c *pathCache) set(fileID string, entry pathEntry, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry.expires.IsZero() {
		entry.expires = now.Add(c.ttl)
	}
	if _, exists := c.entries[fileID]; !exists && len(c.entries) >= c.maxSize {
		c.evictLocked(now)
	}
	c.entries[fileID] = entry
}

// delete 移除缓存项，路径失效时调用。
func (c *pathCache) delete(fileID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, fileID)
}

// evictLocked 在持锁状态下腾出至少一个位置。
func (c *pathCache) evictLocked(now time.Time) {
	for key, entry := range c.entries {
		if !now.Before(entry.expires) {
			delete(c.entries, key)
		}
	}
	if len(c.entries) < c.maxSize {
		return
	}
	oldestKey := ""
	var oldest time.Time
	for key, entry := range c.entries {
		if oldestKey == "" || entry.expires.Before(oldest) {
			oldestKey, oldest = key, entry.expires
		}
	}
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

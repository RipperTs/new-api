package service

import (
	"errors"
	"fmt"
	"one-api/common"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CodexSessionCache 用于缓存 Codex 渠道的 Session ID 和 Conversation ID
// 按照 channel_id 维度缓存,避免频繁变换设备指纹触发风控
type CodexSessionCache struct {
	SessionID      string `json:"session_id"`
	ConversationID string `json:"conversation_id"`
	ExpiresAt      int64  `json:"expires_at"` // Unix 时间戳(秒)
}

const (
	// CodexSessionCacheTTL 缓存过期时间:1小时 (对齐 CLIProxyAPI)
	CodexSessionCacheTTL = 1 * time.Hour
	// CodexSessionCacheCleanupInterval 内存缓存清理间隔:15分钟
	CodexSessionCacheCleanupInterval = 15 * time.Minute
)

var (
	// 内存缓存: channel_id -> CodexSessionCache
	codexSessionMemCache    = make(map[int]*CodexSessionCache)
	codexSessionMemCacheMu  sync.RWMutex
	codexSessionCleanupOnce sync.Once
)

// codexSessionCacheKey 生成 Redis 缓存键
func codexSessionCacheKey(channelID int) string {
	return fmt.Sprintf("codex:session:channel:%d", channelID)
}

// startCodexSessionCacheCleanup 启动内存缓存清理协程
func startCodexSessionCacheCleanup() {
	go func() {
		ticker := time.NewTicker(CodexSessionCacheCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			purgeExpiredCodexSessionCache()
		}
	}()
}

// purgeExpiredCodexSessionCache 清理过期的内存缓存
func purgeExpiredCodexSessionCache() {
	now := time.Now().Unix()
	codexSessionMemCacheMu.Lock()
	defer codexSessionMemCacheMu.Unlock()
	for channelID, cache := range codexSessionMemCache {
		if cache.ExpiresAt > 0 && cache.ExpiresAt < now {
			delete(codexSessionMemCache, channelID)
		}
	}
}

// CodexGetOrCreateSessionCache 获取或创建 Codex Session 缓存
// channelID: 渠道ID
// 返回: session_id, conversation_id, error
func CodexGetOrCreateSessionCache(channelID int) (string, string, error) {
	if channelID <= 0 {
		return "", "", errors.New("invalid channel_id")
	}

	// 1. 尝试从 Redis 获取
	if common.RedisEnabled && common.RDB != nil {
		key := codexSessionCacheKey(channelID)
		var cached CodexSessionCache
		if err := codexLoadJSON(key, &cached); err == nil {
			// 检查是否过期 (预留 60s 缓冲)
			if cached.ExpiresAt > time.Now().Add(60*time.Second).Unix() {
				if cached.SessionID != "" && cached.ConversationID != "" {
					return cached.SessionID, cached.ConversationID, nil
				}
			}
		}
	}

	// 2. 尝试从内存缓存获取
	codexSessionCleanupOnce.Do(startCodexSessionCacheCleanup)
	codexSessionMemCacheMu.RLock()
	cached, ok := codexSessionMemCache[channelID]
	codexSessionMemCacheMu.RUnlock()

	now := time.Now().Unix()
	if ok && cached != nil {
		// 检查是否过期 (预留 60s 缓冲)
		if cached.ExpiresAt > now+60 {
			if cached.SessionID != "" && cached.ConversationID != "" {
				return cached.SessionID, cached.ConversationID, nil
			}
		}
	}

	// 3. 缓存未命中或已过期,生成新的 Session ID
	sessionID := uuid.New().String()
	conversationID := sessionID // conversation_id 与 session_id 相同

	newCache := &CodexSessionCache{
		SessionID:      sessionID,
		ConversationID: conversationID,
		ExpiresAt:      time.Now().Add(CodexSessionCacheTTL).Unix(),
	}

	// 4. 写入 Redis (优先)
	if common.RedisEnabled && common.RDB != nil {
		key := codexSessionCacheKey(channelID)
		_ = codexStoreJSON(key, newCache, CodexSessionCacheTTL)
	}

	// 5. 写入内存缓存 (fallback)
	codexSessionMemCacheMu.Lock()
	codexSessionMemCache[channelID] = newCache
	codexSessionMemCacheMu.Unlock()

	return sessionID, conversationID, nil
}

// CodexInvalidateSessionCache 使指定渠道的 Session 缓存失效
// 用于异常场景 (如上游返回 401/403) 时强制刷新
func CodexInvalidateSessionCache(channelID int) {
	if channelID <= 0 {
		return
	}

	// 清除 Redis 缓存
	if common.RedisEnabled && common.RDB != nil {
		key := codexSessionCacheKey(channelID)
		codexDel(key)
	}

	// 清除内存缓存
	codexSessionMemCacheMu.Lock()
	delete(codexSessionMemCache, channelID)
	codexSessionMemCacheMu.Unlock()
}

// CodexGetSessionCacheStats 获取缓存统计信息 (用于调试)
func CodexGetSessionCacheStats() map[string]interface{} {
	codexSessionMemCacheMu.RLock()
	defer codexSessionMemCacheMu.RUnlock()

	now := time.Now().Unix()
	activeCount := 0
	expiredCount := 0

	for _, cache := range codexSessionMemCache {
		if cache.ExpiresAt > now {
			activeCount++
		} else {
			expiredCount++
		}
	}

	return map[string]interface{}{
		"total_entries":            len(codexSessionMemCache),
		"active_entries":           activeCount,
		"expired_entries":          expiredCount,
		"cleanup_interval_minutes": CodexSessionCacheCleanupInterval.Minutes(),
		"cache_ttl_hours":          CodexSessionCacheTTL.Hours(),
	}
}

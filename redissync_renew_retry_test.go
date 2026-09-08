package redissync

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisSyncRenewRetriesTransientRedisError(t *testing.T) {
	rdb := newIntegrationRedisClient(t)
	const key = "redissync:test:renew-retry"
	cleanupRedisKey(t, rdb, key)

	logger := &renewRetryLogger{infos: make(chan struct{}, 1), errors: make(chan string, 1)}
	hook := &failRefreshHook{}
	hook.remaining.Store(4)
	rdb.AddHook(hook)

	s := NewRedisSync(rdb).SetLogger(logger)
	lock, err := s.Lock(key)
	if err != nil {
		t.Fatalf("lock failed: %v", err)
	}
	t.Cleanup(func() { _ = lock.Unlock() })

	hook.enabled.Store(true)
	select {
	case <-logger.infos:
	case errLog := <-logger.errors:
		t.Fatalf("renewal did not retry the transient Redis error: %s", errLog)
	case <-time.After(lock.ttl/3 + 7*time.Second):
		t.Fatal("renewal did not succeed in the first renewal period")
	}
}

type renewRetryLogger struct {
	infos  chan struct{}
	errors chan string
}

func (l *renewRetryLogger) Info(format string, v ...any) {
	if strings.Contains(format, "锁续期成功") {
		select {
		case l.infos <- struct{}{}:
		default:
		}
	}
}

func (l *renewRetryLogger) Error(format string, v ...any) {
	if strings.Contains(format, "锁续期失败") {
		select {
		case l.errors <- format:
		default:
		}
	}
}

func (*renewRetryLogger) Debug(string, ...any) {}

type failRefreshHook struct {
	enabled   atomic.Bool
	remaining atomic.Int64
}

func (*failRefreshHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *failRefreshHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if h.enabled.Load() && cmd.Name() == "evalsha" && h.remaining.Add(-1) >= 0 {
			return errors.New("temporary redis refresh failure")
		}
		return next(ctx, cmd)
	}
}

func (*failRefreshHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

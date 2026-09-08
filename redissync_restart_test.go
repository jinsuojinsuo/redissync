package redissync

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

func TestRedisSyncRenewAfterRedisRestart(t *testing.T) {
	if os.Getenv("REDISSYNC_RESTART_TEST") != "1" {
		t.Skip("set REDISSYNC_RESTART_TEST=1 to allow restarting local Redis")
	}

	brew, err := exec.LookPath("brew")
	if err != nil {
		t.Skip("Homebrew is required to restart local Redis")
	}

	rdb := newRestartRedisClient(t)
	const key = "redissync:test:restart"
	cleanupRedisKey(t, rdb, key)

	logger := &restartTestLogger{}
	s := NewRedisSync(rdb).SetLogger(logger)
	lock, err := s.Lock(key)
	if err != nil {
		t.Fatalf("lock failed: %v", err)
	}
	unlocked := false
	t.Cleanup(func() {
		if unlocked == false {
			_ = lock.Unlock()
		}
	})
	t.Cleanup(func() {
		if output, err := runBrewRedisService(brew, "start"); err != nil {
			t.Logf("ensure Redis started failed: %v, output=%s", err, output)
		}
	})

	if output, err := runBrewRedisService(brew, "stop"); err != nil {
		t.Fatalf("stop Redis failed: %v, output=%s", err, output)
	}
	waitRestartLog(t, logger, "锁续期失败 key:")

	if output, err := runBrewRedisService(brew, "start"); err != nil {
		t.Fatalf("start Redis failed: %v, output=%s", err, output)
	}
	waitRedisAvailable(t, rdb)

	waitRestartLog(t, logger, "锁续期失败键不存在")
	if err := lock.Unlock(); err != nil {
		t.Logf("unlock after Redis restart: %v", err)
	}
	unlocked = true
	if keys := s.GetM(); len(keys) != 0 {
		t.Fatalf("local lock map leaked after Redis restart: %v", keys)
	}
}

func waitRedisAvailable(t *testing.T, rdb *redis.Client) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := rdb.Ping(ctx).Err()
		cancel()
		if err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("Redis did not become available after restart")
}

func newRestartRedisClient(t *testing.T) *redis.Client {
	t.Helper()

	addr := os.Getenv("REDISSYNC_REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     os.Getenv("REDISSYNC_REDIS_PASSWORD"),
		DB:           1,
		DialTimeout:  time.Second,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
		MaxRetries:   0,
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		t.Fatalf("Redis is not available at %s: %v", addr, err)
	}
	t.Cleanup(func() {
		_ = rdb.Close()
	})
	return rdb
}

func runBrewRedisService(brew, action string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, brew, "services", action, "redis").CombinedOutput()
}

func waitRestartLog(t *testing.T, logger *restartTestLogger, message string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if logger.contains(message) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("did not receive log %q; logs=%v", message, logger.logs())
}

type restartTestLogger struct {
	mu       sync.Mutex
	messages []string
}

func (l *restartTestLogger) Info(format string, v ...any) {
	l.add(format, v...)
}

func (l *restartTestLogger) Debug(format string, v ...any) {
	l.add(format, v...)
}

func (l *restartTestLogger) Error(format string, v ...any) {
	l.add(format, v...)
}

func (l *restartTestLogger) add(format string, v ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, fmt.Sprintf(format, v...))
}

func (l *restartTestLogger) contains(message string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, log := range l.messages {
		if strings.Contains(log, message) {
			return true
		}
	}
	return false
}

func (l *restartTestLogger) logs() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.messages...)
}

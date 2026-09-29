package redissync

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
)

// 丢弃成功响应，模拟 Redis 已提交而调用方只收到超时。
type lostAcquireReplyHook struct {
	key         string
	cancel      context.CancelFunc
	afterCommit func()
	cleanupErr  error
	fired       bool
}

func (h *lostAcquireReplyHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if h.fired && h.cleanupErr != nil && (cmd.Name() == "eval" || cmd.Name() == "evalsha") {
		return ctx, h.cleanupErr
	}
	return ctx, nil
}

func (h *lostAcquireReplyHook) AfterProcess(ctx context.Context, cmd redis.Cmder) error {
	if h.fired || cmd.Err() != nil {
		return nil
	}
	if cmd.Name() != "set" && cmd.Name() != "eval" && cmd.Name() != "evalsha" {
		return nil
	}
	hasKey := false
	for _, arg := range cmd.Args() {
		if arg == h.key {
			hasKey = true
		}
	}
	if !hasKey {
		return nil
	}
	h.fired = true
	if h.afterCommit != nil {
		h.afterCommit()
	}
	h.cancel()
	return context.DeadlineExceeded
}

func (*lostAcquireReplyHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (*lostAcquireReplyHook) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

func newCleanupTestClient(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	m := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: m.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return m, rdb
}

func TestAcquireReplyTimeoutCleansOwnLock(t *testing.T) {
	m, rdb := newCleanupTestClient(t)
	const key = "lock:lost-response"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	hook := &lostAcquireReplyHook{key: key, cancel: cancel}
	rdb.AddHook(hook)
	s := NewRedisSync(rdb)
	lock, err := s.LockContext(ctx, key)
	if lock != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock=%v err=%v", lock, err)
	}
	if !hook.fired {
		t.Fatal("successful write was not intercepted")
	}
	if m.Exists(key) {
		t.Fatal("acquire timed out but its Redis lock remains")
	}
	if len(s.GetM()) != 0 {
		t.Fatal("local waiter leaked")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	lock, err = s.LockContext(ctx2, key)
	if err != nil {
		t.Fatalf("subsequent acquire blocked: %v", err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if m.Exists(key) {
		t.Fatal("unlock left the Redis key")
	}
}

func TestAcquireReplyTimeoutDoesNotDeleteNewOwner(t *testing.T) {
	m, rdb := newCleanupTestClient(t)
	const key = "lock:replacement-owner"
	const otherOwner = "different-token-and-metadata"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rdb.AddHook(&lostAcquireReplyHook{key: key, cancel: cancel, afterCommit: func() { _ = m.Set(key, otherOwner) }})
	_, err := NewRedisSync(rdb).LockContext(ctx, key)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if got, err := m.Get(key); err != nil || got != otherOwner {
		t.Fatalf("other owner changed: value=%q err=%v", got, err)
	}
}

func TestAcquireContentionPreservesOwnerAndLockLifecycle(t *testing.T) {
	m, rdb := newCleanupTestClient(t)
	const key = "lock:lifecycle"
	owner, err := NewRedisSync(rdb).Lock(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !owner.unlocked.Load() {
			_ = owner.Unlock()
		}
	})
	value, _ := m.Get(key)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if lock, err := NewRedisSync(rdb).LockContext(ctx, key); lock != nil || err == nil {
		t.Fatalf("contender obtained lock: %v %v", lock, err)
	}
	if got, _ := m.Get(key); got != value {
		t.Fatal("contender changed owner's token")
	}
	m.FastForward(time.Second)
	if err := owner.rdl.Refresh(context.Background(), owner.ttl, nil); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if m.TTL(key) != owner.ttl {
		t.Fatal("refresh did not extend the lock")
	}
	if err := owner.Unlock(); err != nil {
		t.Fatal(err)
	}
	if m.Exists(key) {
		t.Fatal("unlock did not delete key")
	}
}

// 在真实 go-redis 读响应处注入一次断线，让客户端自身执行自动重试。
type dropAcquireReplyConn struct {
	net.Conn
	dropped   *atomic.Bool
	afterDrop func()
	reader    *bufio.Reader
	pending   []byte
	readErr   error
}

func (c *dropAcquireReplyConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if c.reader == nil {
		c.reader = bufio.NewReader(c.Conn)
	}
	if len(c.pending) == 0 {
		c.pending, c.readErr = c.reader.ReadBytes('\n')
		if c.readErr == nil && (string(c.pending) == "+OK\r\n" || string(c.pending) == ":1\r\n") && c.dropped.CompareAndSwap(false, true) {
			if c.afterDrop != nil {
				c.afterDrop()
			}
			c.pending = nil
			return 0, io.ErrUnexpectedEOF
		}
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	if len(c.pending) != 0 {
		return n, nil
	}
	return n, c.readErr
}

func TestAcquireAutomaticRetryRecognizesOwnToken(t *testing.T) {
	m := miniredis.RunT(t)
	var dropped atomic.Bool
	rdb := redis.NewClient(&redis.Options{Addr: m.Addr(), Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &dropAcquireReplyConn{Conn: conn, dropped: &dropped, afterDrop: func() {
			m.FastForward(m.TTL("lock:retry-response") - time.Millisecond)
		}}, nil
	}})
	defer rdb.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lock, err := NewRedisSync(rdb).LockContext(ctx, "lock:retry-response")
	if err != nil {
		t.Fatalf("retry failed to recognize committed lock: %v", err)
	}
	defer lock.Unlock()
	if !dropped.Load() {
		t.Fatal("successful wire response was not dropped")
	}
	if m.TTL("lock:retry-response") < 20*time.Second {
		t.Fatal("retry returned a lock about to expire")
	}
	value, _ := m.Get("lock:retry-response")
	if !strings.HasPrefix(value, lock.Token()) {
		t.Fatal("returned lock does not own stored token")
	}
}

type cleanupTestLogger struct{ errors []string }

func (*cleanupTestLogger) Info(string, ...any)  {}
func (*cleanupTestLogger) Debug(string, ...any) {}
func (l *cleanupTestLogger) Error(format string, args ...any) {
	l.errors = append(l.errors, fmt.Sprintf(format, args...))
}

func TestAcquireCleanupFailurePreservesOriginalError(t *testing.T) {
	m, rdb := newCleanupTestClient(t)
	const key = "lock:cleanup-failure"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rdb.AddHook(&lostAcquireReplyHook{key: key, cancel: cancel, cleanupErr: errors.New("cleanup unavailable")})
	logger := &cleanupTestLogger{}
	s := NewRedisSync(rdb).SetLogger(logger)
	_, err := s.LockContext(ctx, key)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("original error lost: %v", err)
	}
	if len(logger.errors) != 1 || !strings.Contains(logger.errors[0], key) || !strings.Contains(logger.errors[0], "cleanup unavailable") {
		t.Fatalf("missing cleanup diagnostic: %v", logger.errors)
	}
	if !m.Exists(key) || m.TTL(key) <= 0 {
		t.Fatal("cleanup failure should retain TTL fallback")
	}
	if len(s.GetM()) != 0 {
		t.Fatal("cleanup failure leaked local waiter")
	}
}

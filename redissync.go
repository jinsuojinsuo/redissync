package redissync

import (
	"context"
	"fmt"
	"github.com/bsm/redislock"
	"github.com/go-redis/redis/v8"
	"math/rand"
	"path"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

var (
	// ErrNotObtained 当无法获得锁时，将返回ErrNotObtained
	ErrNotObtained = redislock.ErrNotObtained

	// ErrLockNotHeld 尝试释放非活动锁时返回ErrLockNotHeld
	ErrLockNotHeld = redislock.ErrLockNotHeld
)

type Logger interface {
	Println(v ...any)
}

type Lock struct {
	rdl    *redislock.Lock
	ctx    context.Context
	cancel context.CancelFunc //取消函数
	key    string             //加锁的key
	ttl    time.Duration      //redis中的键保存时长
	logger Logger             //用于记录错误日志
}

// Unlock 解锁
func (l *Lock) Unlock() error {
	defer l.cancel()
	if err := l.rdl.Release(l.ctx); err == redislock.ErrLockNotHeld {
		return nil //未加锁不需要解锁
	} else if err != nil {
		return err
	}
	return nil
}

// Token 获取锁中存的值
func (l *Lock) Token() string {
	return l.rdl.Token()
}

type RedisSync struct {
	redisLockClient *redislock.Client
	logMode         bool //true开启日志 false关闭日志
	logger          Logger
	metadata        string
}

func NewRedisSync(rdb *redis.Client) *RedisSync {
	return &RedisSync{
		redisLockClient: redislock.New(rdb),
	}
}

func (s *RedisSync) SetMetadata(metadata string) *RedisSync {
	s.metadata = metadata
	return s
}

// SetLogger 设置日志
func (s *RedisSync) SetLogger(logger Logger) *RedisSync {
	s.logger = logger
	return s
}

// TryLock 尝试加锁，非阻塞，加锁失败直接返回
// ttl 设置加锁时长,设置redis键的过期时间
// 可以自动续期
func (s *RedisSync) TryLock(key string, ttl time.Duration) (*Lock, error) {
	//ttl不能小于1秒
	if ttl < time.Second {
		ttl = time.Second
	}

	if s.metadata == "" {
		s.metadata = getParentCaller()
	}

	ctx, cancel := context.WithCancel(context.Background())
	rdlLock, err := s.redisLockClient.Obtain(ctx, key, ttl, &redislock.Options{
		RetryStrategy: redislock.NoRetry(), //不重试
		Metadata:      s.metadata,
	})
	if err != nil {
		cancel()
		return nil, err
	}
	l := &Lock{
		rdl:    rdlLock,
		ctx:    ctx,
		key:    key,
		cancel: cancel,
		ttl:    ttl,
	}
	s.renewExpirationScheduler(l) //自动续期程序
	return l, nil
}

// Lock 阻塞获取锁，直到获取成功或遇到错误才返回
// 可以自动续期
func (s *RedisSync) Lock(key string) (*Lock, error) {
	ttl := time.Second * time.Duration(rand.Intn(11)+20)                                //存储时长秒 最少25秒 最大30秒
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*86400*365*100) //这里设置超时时间为100年,也就是必须获取到锁才返回，否则一直阻塞

	var err error
	var rdlLock *redislock.Lock

	if s.metadata == "" {
		s.metadata = getParentCaller()
	}

	rdlLock, err = s.redisLockClient.Obtain(ctx, key, ttl, &redislock.Options{
		//重试策略 默认最多只等待ttl秒或设置 context.WithTimeout 来控制尝试时长
		RetryStrategy: redislock.ExponentialBackoff(time.Millisecond*100, time.Second*10), //指数退避算法(最小间隔时间，最大间隔时间)
		Metadata:      s.metadata,
	})
	if err != nil {
		cancel()
		return nil, err
	}

	l := &Lock{
		rdl:    rdlLock,
		ctx:    ctx,
		key:    key,
		cancel: cancel,
		ttl:    ttl,
	}
	s.renewExpirationScheduler(l) //自动续期程序
	return l, nil
}

// 自动续期程序
func (s *RedisSync) renewExpirationScheduler(l *Lock) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.println(fmt.Sprintf("锁续期失败: %+v %+v", l.key, debug.Stack()))
			}
		}()

	For:
		for {
			time.Sleep(l.ttl / 3) //阻塞存储时长的2分之一
			select {
			case <-l.ctx.Done():
				s.println(fmt.Sprintf("锁续期已经解锁: %+v", l.key))
				break For //已经解锁 跳出for循环
			default:
				if err := l.rdl.Refresh(l.ctx, l.ttl, nil); err == redislock.ErrNotObtained {
					s.println(fmt.Sprintf("锁续期失败键不存在: %+v %+v", l.key, err))
					break For
				} else if err != nil {
					s.println(fmt.Sprintf("锁续期失败: %+v %+v", l.key, err))
				} else {
					s.println(fmt.Sprintf("锁续期成功: %+v", l.key))
				}
			}
		}
	}()
}

// 日志打印
func (s *RedisSync) println(v ...any) {
	if s.logger != nil {
		s.logger.Println(v...)
	}
}

// GetParentCaller 获取父级调用者所在行号
func getParentCaller(dirLevel ...int) string {
	s := 3 //默认为父级调用者
	if len(dirLevel) > 0 {
		s = dirLevel[0]
	}
	_, file, line, ok := runtime.Caller(2)
	if ok == true {
		return fmt.Sprintf("%v:%v", pathRetainRight(file, s), line)
	} else {
		return ""
	}
}

// PathRetainRight 保留n级目录
func pathRetainRight(pathStr string, n int) string {
	rt := ""
	for i := 0; i < n; i++ {
		b := path.Base(pathStr)
		if b == "." {
			break
		}
		rt = b + "/" + rt
		pathStr = path.Dir(pathStr)
	}
	return strings.Trim(rt, "/")
}

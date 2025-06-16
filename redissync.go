package redissync

import (
	"context"
	"errors"
	"fmt"
	"github.com/bsm/redislock"
	"github.com/go-redis/redis/v8"
	"math/rand"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

type Logger interface {
	Info(format string, v ...any)  //info日志
	Error(format string, v ...any) //错误日志
}

type RedisSync struct {
	redisLockClient *redislock.Client
	m               map[string]*t1 //每个锁一个key 问题:所有线程都解锁后无法释放map的key
	lock            sync.Mutex     //互斥锁 用户锁m,m虽然是支持并发的map但又可能需要同时操作两次
	logMode         bool           //true开启日志 false关闭日志
	logger          Logger
}

func NewRedisSync(rdb *redis.Client) *RedisSync {
	return &RedisSync{
		redisLockClient: redislock.New(rdb),
		m:               map[string]*t1{},
	}
}

func (s *RedisSync) GetM() []string {
	s.lock.Lock()
	s.lock.Unlock()
	list := []string{}
	for k, _ := range s.m {
		list = append(list, k)
	}
	return list
}

// 日志打印
func (s *RedisSync) error(format string, v ...any) {
	if s.logger != nil {
		s.logger.Error(format, v...)
	}
}

func (s *RedisSync) info(format string, v ...any) {
	if s.logger != nil {
		s.logger.Info(format, v...)
	}
}

// SetLogger 设置日志
func (s *RedisSync) SetLogger(logger Logger) *RedisSync {
	s.logger = logger
	return s
}

type t1 struct {
	ch  chan struct{}
	num atomic.Int64
}

// Lock 阻塞获取锁，直到获取成功或遇到错误才返回
// 可以自动续期
func (s *RedisSync) Lock(key string) (*Lock, error) {
	t1Obj := &t1{}
	s.lock.Lock()
	if _, ok := s.m[key]; ok == false {
		s.m[key] = &t1{ch: make(chan struct{}, 1), num: atomic.Int64{}}
	}
	t1Obj = s.m[key]
	t1Obj.num.Add(1)
	s.lock.Unlock()

	Hostname, _ := os.Hostname()
	metadata := fmt.Sprintf("%s_%s", Hostname, getParentCaller())

	s.info("阻塞等待1 key:%s metadata:%s", key, metadata)
	t1Obj.ch <- struct{}{}
	s.info("阻塞等待2 key:%s metadata:%s", key, metadata)

	ttl := time.Second * time.Duration(rand.Intn(11)+20)                                //存储时长秒 最少20秒 最大30秒
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*86400*365*100) //这里设置超时时间为100年,也就是必须获取到锁才返回，否则一直阻塞

	rdlLock, err := s.redisLockClient.Obtain(ctx, key, ttl, &redislock.Options{
		//重试策略 默认最多只等待ttl秒或设置 context.WithTimeout 来控制尝试时长
		RetryStrategy: redislock.LinearBackoff(time.Millisecond * 50), //100毫秒重试1次
		Metadata:      metadata,
	})
	if err != nil {
		cancel() //获取锁失败 走到这一般是redis重启之类的
		return nil, err
	}

	s.info("加锁成功 key:%s metadata:%s", key, metadata)

	l := &Lock{
		redisSync: s,
		rdl:       rdlLock,
		ctx:       ctx,
		key:       key,
		cancel:    cancel,
		ttl:       ttl,
		metadata:  metadata,
	}
	s.renewExpirationScheduler(l) //自动续期程序
	return l, nil
}

// 自动续期程序
func (s *RedisSync) renewExpirationScheduler(l *Lock) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.error("锁续期失败 key:%s stack:%s", l.key, debug.Stack())
			}
		}()

	For:
		for {
			time.Sleep(l.ttl / 3) //阻塞存储时长的2分之一
			select {
			case <-l.ctx.Done():
				s.info("锁续期已经解锁: %s", l.key)
				break For //已经解锁 跳出for循环
			default:
				if err := l.rdl.Refresh(l.ctx, l.ttl, nil); errors.Is(err, redislock.ErrNotObtained) {
					s.error("锁续期失败键不存在 key:%s err:%+v", l.key)
					break For
				} else if err != nil {
					s.error("锁续期失败: key:%s err:%+v", l.key, err)
				} else {
					s.info("锁续期成功 key:%s", l.key)
				}
			}
		}
	}()
}

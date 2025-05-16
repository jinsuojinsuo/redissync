package redissync

import (
	"context"
	"errors"
	"fmt"
	"github.com/bsm/redislock"
	"time"
)

type Lock struct {
	redisSync *RedisSync
	rdl       *redislock.Lock
	ctx       context.Context
	cancel    context.CancelFunc //取消函数
	key       string             //加锁的key
	ttl       time.Duration      //redis中的键保存时长
	logger    Logger             //用于记录错误日志
	metadata  string
}

// Unlock 解锁
func (l *Lock) Unlock() error {
	defer l.cancel()
	if err := l.rdl.Release(l.ctx); errors.Is(err, redislock.ErrLockNotHeld) {
		l.redisSync.error("未加锁不需要解锁,有可能锁被手动删除 key:%s", l.key)
		//未加锁不需要解锁
		//此处不能直接return,因为 如果redis中的key被手动删除,value.ch 必须要释放，否则会死锁
	} else if err != nil {
		return err
	}

	l.redisSync.lock.Lock()
	defer l.redisSync.lock.Unlock()
	value, ok := l.redisSync.m[l.key] //获取
	if ok == false {
		return fmt.Errorf("解锁异常1")
	}

	<-value.ch //通知下一个线程获取锁
	num := value.num.Add(-1)
	if num == 0 {
		delete(l.redisSync.m, l.key)
	} else if num < 0 {
		panic(fmt.Errorf("解锁异常2 %d", num))
	}

	return nil
}

// Token 获取锁中存的值
func (l *Lock) Token() string {
	return l.rdl.Token()
}

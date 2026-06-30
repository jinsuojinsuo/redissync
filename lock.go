package redissync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bsm/redislock"
)

type Lock struct {
	redisSync    *RedisSync
	rdl          *redislock.Lock
	UnLockCtx    context.Context
	UnLockCancel context.CancelFunc //取消函数
	key          string             //加锁的key
	ttl          time.Duration      //redis中的键保存时长
	logger       Logger             //用于记录错误日志
	metadata     string
}

// Unlock 解锁
func (l *Lock) Unlock() (err error) {
	defer l.UnLockCancel()

	defer func() {
		//不管redis锁能不能解开,内存锁都一定要解开,因为redis锁解不开可以手动删除，内存锁解不开只能重启进程
		//就算内存锁解了，redis锁没解，也可以做到手动删除redis锁，
		//解除了内存锁只要redis锁没解，也不会造成并发执行，因为redis锁会阻塞等锁
		if err2 := l.unlockMem(); err2 != nil {
			err = errors.Join(err, err2)
		}
	}()

	if err := l.rdl.Release(l.UnLockCtx); errors.Is(err, redislock.ErrLockNotHeld) {
		l.redisSync.error("未加锁不需要解锁,有可能锁被手动删除 key:%s", l.key)
		//未加锁不需要解锁
		//此处不能直接return,因为 如果redis中的key被手动删除,value.ch 必须要释放，否则会死锁
	} else if err != nil {
		return err
	}

	return nil
}

// 解内存锁
func (l *Lock) unlockMem() error {
	l.redisSync.lock.Lock()
	defer l.redisSync.lock.Unlock()
	value, ok := l.redisSync.m[l.key] //获取
	if ok == false {
		return fmt.Errorf("解锁异常1")
	}

	select {
	case <-value.ch: //通知下一个线程获取锁
	default:
		//无需处理
	}
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

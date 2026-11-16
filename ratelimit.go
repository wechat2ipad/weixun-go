package weixun

import (
	"context"
	"sync"
	"time"
)

// RateLimiter 是令牌桶限速器，同时保证同一账号的请求串行执行。
//
// 为什么 SDK 必须自带限速：协议端实测**没有任何服务端限流**
// （全仓无 token bucket、无 429、业务执行在锁外）。
// 也就是说客户如果并发猛发，直接就是被微信风控，而不是被 API 拒绝。
//
// 默认值来自官方风控建议（customer-gateway/public/risk-guide.html）：
//   - 单账号 40 条/分钟
//   - 切换不同用户间隔 ≥1s
//   - 切换不同群 2~5s
type RateLimiter struct {
	mu       sync.Mutex
	capacity int           // 桶容量
	interval time.Duration // 补充一个令牌的间隔
	tokens   float64
	last     time.Time
}

// NewRateLimiter 创建限速器。perWindow 时间内最多 capacity 次。
func NewRateLimiter(capacity int, perWindow time.Duration) *RateLimiter {
	if capacity <= 0 {
		return nil
	}
	return &RateLimiter{
		capacity: capacity,
		interval: perWindow / time.Duration(capacity),
		tokens:   float64(capacity),
		last:     time.Now(),
	}
}

// Wait 阻塞直到获取一个令牌，或 ctx 取消。
func (r *RateLimiter) Wait(ctx context.Context) error {
	if r == nil {
		return nil
	}
	for {
		r.mu.Lock()
		now := time.Now()
		// 按经过时间补充令牌
		elapsed := now.Sub(r.last)
		r.tokens += elapsed.Seconds() / r.interval.Seconds()
		if r.tokens > float64(r.capacity) {
			r.tokens = float64(r.capacity)
		}
		r.last = now

		if r.tokens >= 1 {
			r.tokens--
			r.mu.Unlock()
			return nil
		}
		// 计算需要等待多久才能凑够一个令牌
		need := (1 - r.tokens) * r.interval.Seconds()
		r.mu.Unlock()

		wait := time.Duration(need * float64(time.Second))
		if wait < time.Millisecond {
			wait = time.Millisecond
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// SerialQueue 保证同一账号的操作串行执行（协议端不保证串行）。
type SerialQueue struct {
	mu sync.Mutex
}

// Do 串行执行 fn。
func (q *SerialQueue) Do(fn func()) {
	q.mu.Lock()
	defer q.mu.Unlock()
	fn()
}

// Throttle 在两次调用之间强制最小间隔，用于「切换不同用户 ≥1s」这类约束。
type Throttle struct {
	mu   sync.Mutex
	last time.Time
	min  time.Duration
}

// NewThrottle 创建一个最小间隔限流器。
func NewThrottle(min time.Duration) *Throttle {
	return &Throttle{min: min}
}

// Wait 阻塞到距离上次调用已满 min。
func (t *Throttle) Wait(ctx context.Context) error {
	if t == nil || t.min <= 0 {
		return nil
	}
	t.mu.Lock()
	elapsed := time.Since(t.last)
	t.last = time.Now()
	t.mu.Unlock()

	if elapsed >= t.min {
		return nil
	}
	wait := t.min - elapsed
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}

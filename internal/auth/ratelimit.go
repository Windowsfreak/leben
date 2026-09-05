package auth

import (
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
	rate   float64 // tokens per second
	max    float64
}

// RateLimiter is an in-memory token-bucket limiter keyed by scope+client.
// Sufficient for this single-process deployment; a reverse proxy would be
// needed if the app ever scaled horizontally.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{buckets: make(map[string]*bucket)}
	go func() {
		for range time.Tick(10 * time.Minute) {
			rl.mu.Lock()
			for k, b := range rl.buckets {
				if time.Since(b.last) > time.Hour {
					delete(rl.buckets, k)
				}
			}
			rl.mu.Unlock()
		}
	}()
	return rl
}

// Allow reports whether one event for key is permitted right now.
func (rl *RateLimiter) Allow(key string, perMinute, burst int) bool {
	if perMinute <= 0 || burst <= 0 {
		return true
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &bucket{
			tokens: float64(burst),
			max:    float64(burst),
			rate:   float64(perMinute) / 60.0,
			last:   now,
		}
		rl.buckets[key] = b
	}

	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * b.rate
	if b.tokens > b.max {
		b.tokens = b.max
	}
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

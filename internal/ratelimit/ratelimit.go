// Package ratelimit gives each API key its own independent request budget,
// so one volunteer's traffic can't starve another's or hammer this service.
package ratelimit

import (
	"sync"

	"golang.org/x/time/rate"
)

// Limiter's per-key map grows for every distinct key ever seen and is
// never pruned. Acceptable for v1's expected volunteer count; revisit if
// this service ever sees enough distinct keys for that to matter.
type Limiter struct {
	mu        sync.Mutex
	limiters  map[int64]*rate.Limiter
	perSecond float64
	burst     int
}

// New creates a limiter granting each key burst requests up front, then
// perSecond requests/sec sustained thereafter.
func New(perSecond float64, burst int) *Limiter {
	return &Limiter{
		limiters:  make(map[int64]*rate.Limiter),
		perSecond: perSecond,
		burst:     burst,
	}
}

// Allow reports whether keyID may make a request right now.
func (l *Limiter) Allow(keyID int64) bool {
	l.mu.Lock()
	rl, ok := l.limiters[keyID]
	if !ok {
		rl = rate.NewLimiter(rate.Limit(l.perSecond), l.burst)
		l.limiters[keyID] = rl
	}
	l.mu.Unlock()
	return rl.Allow()
}

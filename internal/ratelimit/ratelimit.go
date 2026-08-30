package ratelimit

import (
	"sync"
	"time"
)

type Limiter struct {
	mu        sync.Mutex
	window    time.Duration
	max       int
	hits      map[string][]time.Time
	lastPurge time.Time
}

func New(window time.Duration, max int) *Limiter {
	if max < 1 {
		max = 1
	}
	if window <= 0 {
		window = time.Second
	}
	return &Limiter{
		window:    window,
		max:       max,
		hits:      make(map[string][]time.Time),
		lastPurge: time.Now(),
	}
}

func (l *Limiter) Allow(key string) bool {
	now := time.Now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastPurge) > l.window {
		for k, ts := range l.hits {
			var keep []time.Time
			for _, t := range ts {
				if t.After(cutoff) {
					keep = append(keep, t)
				}
			}
			if len(keep) == 0 {
				delete(l.hits, k)
			} else {
				l.hits[k] = keep
			}
		}
		l.lastPurge = now
	}

	ts := l.hits[key]
	var keep []time.Time
	for _, t := range ts {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= l.max {
		l.hits[key] = keep
		return false
	}
	l.hits[key] = append(keep, now)
	return true
}

func (l *Limiter) Remaining(key string) int {
	now := time.Now()
	cutoff := now.Add(-l.window)
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			n++
		}
	}
	left := l.max - n
	if left < 0 {
		return 0
	}
	return left
}

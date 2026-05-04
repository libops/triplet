package vips

import "context"

// Limiter bounds concurrent libvips jobs.
type Limiter struct {
	ch chan struct{}
}

// NewLimiter constructs a limiter. Non-positive max values disable limiting.
func NewLimiter(max int) *Limiter {
	if max <= 0 {
		return nil
	}
	return &Limiter{ch: make(chan struct{}, max)}
}

// Acquire waits for a libvips job slot and returns a release function.
func (l *Limiter) Acquire(ctx context.Context) (func(), error) {
	if l == nil || l.ch == nil {
		return func() {}, nil
	}
	select {
	case l.ch <- struct{}{}:
		return func() { <-l.ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

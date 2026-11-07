package buffer

import (
	"context"
	"errors"
	"sync"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

var ErrClosed = errors.New("ingress buffer is closed")
var ErrInvalidCapacity = errors.New("ingress buffer capacity must be greater than zero")

// Buffer owns independent filtered records. Closing wakes blocked producers;
// queued records remain available to consumers.
type Buffer struct {
	queue  chan telemetry.RawRecord
	done   chan struct{}
	space  chan struct{}
	mu     sync.Mutex
	closed bool
}

func New(capacity int) (*Buffer, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Buffer{queue: make(chan telemetry.RawRecord, capacity), done: make(chan struct{}), space: make(chan struct{})}, nil
}
func (b *Buffer) Capacity() int { return cap(b.queue) }
func (b *Buffer) Len() int      { return len(b.queue) }
func (b *Buffer) Enqueue(ctx context.Context, r telemetry.RawRecord) error {
	if r == nil {
		return errors.New("cannot enqueue nil record")
	}
	r = cloneRecord(r)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return ErrClosed
		}
		select {
		case b.queue <- r:
			b.mu.Unlock()
			return nil
		default:
		}
		available := b.space
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.done:
			return ErrClosed
		case <-available:
		}
	}
}
func (b *Buffer) Dequeue(ctx context.Context) (telemetry.RawRecord, error) {
	take := func(r telemetry.RawRecord) (telemetry.RawRecord, error) {
		b.mu.Lock()
		close(b.space)
		b.space = make(chan struct{})
		b.mu.Unlock()
		return r, nil
	}
	select {
	case r := <-b.queue:
		return take(r)
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.done:
		select {
		case r := <-b.queue:
			return take(r)
		default:
			return nil, ErrClosed
		}
	}
}
func (b *Buffer) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed {
		b.closed = true
		close(b.done)
	}
}
func cloneRecord(r telemetry.RawRecord) telemetry.RawRecord {
	out := telemetry.RawRecord{}
	for k, v := range r {
		switch x := v.(type) {
		case []string:
			out[k] = append([]string{}, x...)
		case []any:
			out[k] = append([]any{}, x...)
		default:
			out[k] = v
		}
	}
	return out
}

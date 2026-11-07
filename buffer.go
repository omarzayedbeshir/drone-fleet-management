package buffer

import (
	"context"
	"errors"

	"aerosync/telemetry-pipeline/telemetry"
)

var ErrClosed = errors.New("ingress buffer is closed")
var ErrInvalidCapacity = errors.New("ingress buffer capacity must be greater than zero")

// Buffer is a bounded FIFO ingress queue. Enqueue blocks when full, providing
// backpressure, and respects context cancellation so adapters/workers can stop.
type Buffer struct {
	queue chan telemetry.RawRecord
	done  chan struct{}
}

func New(capacity int) (*Buffer, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Buffer{queue: make(chan telemetry.RawRecord, capacity), done: make(chan struct{})}, nil
}

func (b *Buffer) Capacity() int { return cap(b.queue) }
func (b *Buffer) Len() int      { return len(b.queue) }

// Enqueue blocks while the queue is full. Callers should pass a deadline or
// cancellable context when they cannot wait indefinitely.
func (b *Buffer) Enqueue(ctx context.Context, record telemetry.RawRecord) error {
	if record == nil {
		return errors.New("cannot enqueue a nil record")
	}
	select {
	case <-b.done:
		return ErrClosed
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		return ErrClosed
	case b.queue <- cloneRecord(record):
		return nil
	}
}

// Dequeue blocks until a record is available, the context is cancelled, or
// the buffer is closed and drained.
func (b *Buffer) Dequeue(ctx context.Context) (telemetry.RawRecord, error) {
	select {
	case record := <-b.queue:
		return record, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.done:
		select {
		case record := <-b.queue:
			return record, nil
		default:
			return nil, ErrClosed
		}
	}
}

// Close prevents future enqueues. Already queued records can still be drained.
func (b *Buffer) Close() {
	select {
	case <-b.done:
	default:
		close(b.done)
	}
}

func cloneRecord(in telemetry.RawRecord) telemetry.RawRecord {
	out := make(telemetry.RawRecord, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

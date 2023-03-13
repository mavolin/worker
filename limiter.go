package worker

import (
	"container/list"
	"context"
	"sync"
)

// Limiter is a utility to limit the concurrent execution of a function.
//
// It differs from a [Pool] in that allows execution in the same Goroutine as
// the user, whereas a [Pool] would spawn a separate Goroutine for each worker.
type Limiter struct {
	mut                        sync.Mutex
	numRunning, maxConcurrency int
	queue                      list.List // list.List[chan struct{}]
}

// NewLimiter creates a new Limiter that will at most allow maxConcurrency
// concurrent executions of the functions given Go and friends.
func NewLimiter(maxConcurrency int) *Limiter {
	return &Limiter{maxConcurrency: maxConcurrency}
}

// ============================================================================
// No Return
// ======================================================================================

// Do executes f in the caller's Goroutine.
//
// If there are other functions started by Do and friends running, and the
// amount of functions surpasses the maxConcurrency value of the Limiter, then
// Do will blockingly wait until it another function finishes execution.
//
// Functions given to Do are executed in the order in which Do is called.
//
// Do is safe for concurrent use.
func (l *Limiter) Do(f func()) {
	l.DoContext(context.Background(), f)
}

// Try is the same as [Limiter.Do], but only executes f if the maxConcurrency
// of the limiter allows execution without waiting.
//
// It returns true, if f was executed and false otherwise.
func (l *Limiter) Try(f func()) (ran bool) {
	l.mut.Lock()
	if l.numRunning < l.maxConcurrency {
		l.numRunning++
		l.mut.Unlock()

		f()

		l.mut.Lock()
		if l.queue.Len() > 0 {
			close(l.queue.Front().Value.(chan struct{}))
			l.queue.Remove(l.queue.Front())
		} else {
			l.numRunning--
		}
		l.mut.Unlock()
		return true
	}
	l.mut.Unlock()
	return false
}

// DoContext is the same as [Limiter.Do], but will only wait for as long as the
// passed [context.Context] isn't done.
//
// It returns true, if f was executed, and false if f wasn't due to context
// cancellation/expiration.
func (l *Limiter) DoContext(ctx context.Context, f func()) bool {
	l.mut.Lock()
	if l.numRunning < l.maxConcurrency {
		l.numRunning++
		l.mut.Unlock()

		f()

		l.mut.Lock()
		if l.queue.Len() > 0 {
			close(l.queue.Front().Value.(chan struct{}))
			l.queue.Remove(l.queue.Front())
		} else {
			l.numRunning--
		}
		l.mut.Unlock()
		return true
	}

	wait := make(chan struct{})
	l.queue.PushBack(wait)
	l.mut.Unlock()

	select {
	case <-wait:
	case <-ctx.Done():
		l.mut.Lock()
		select {
		case <-wait:
		default:
			l.queue.Remove(l.queue.Front())
			l.mut.Unlock()
			return false
		}

		// another function yielded its place to us, give it to the next
		// function since our context is done

		if l.queue.Len() > 0 {
			close(l.queue.Front().Value.(chan struct{}))
			l.queue.Remove(l.queue.Front())
		} else {
			l.numRunning--
		}

		l.mut.Unlock()
		return false
	}

	l.mut.Lock()
	l.numRunning++
	l.mut.Unlock()

	f()

	l.mut.Lock()
	if l.queue.Len() > 0 {
		close(l.queue.Front().Value.(chan struct{}))
		l.queue.Remove(l.queue.Front())
	} else {
		l.numRunning--
	}
	l.mut.Unlock()
	return true
}

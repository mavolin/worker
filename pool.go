package worker

import (
	"container/list"
	"context"
	"errors"
	"sync"
)

// Pool represents a worker pool that can be used to limit concurrency,
// collect and act on errors, allow workers to last task execution, and
// act as a [sync.WaitGroup].
//
// Refer to [NewPool] for an explanation of the pools inner workings.
//
// If you require collection of result values, use [ResultPool].
type Pool struct {
	tasks chan Task // Go is called and sends task to manager
	// if maxWorkers reached, task is queued, otherwise ->
	work chan Task // manger sends task to worker

	errChan chan error // worker finishes tasks and reports (nil) error to manager

	kill chan struct{} // manager receives err, if no tasks are queued -> kill the idle worker

	ctx      context.Context
	cancel   context.CancelFunc
	last     chan struct{} // closed by Last to indicate that no new tasks should be accepted
	stopOnce sync.Once

	done chan struct{} // closed by manager once ctx.Done() is closed and all workers have exited

	errCollector       Collector[error]
	capturedContextErr bool

	// CancelOnPanic, if true, will cancel the context of all other workers
	// and last accepting new work, if a worker panics.
	//
	// If left false, the pool will continue to process new work.
	CancelOnPanic bool
}

// Task is the function given to a [Pool].
//
// It may return an error, which is collected and then returned by [Pool.Wait].
//
// If Task returns true for cancel, the pool will cancel the context given
// to all running Tasks.
// It is the same as calling the cancel func of the NewPool directly, however,
// the cancel bool was added, as it is more explicit and doesn't require
// access to the original's context cancel func.
type Task func(context.Context) (err error, cancel bool)

// NewPool creates a new [Pool] that will run at most maxWorkers workers and
// additionally a single manager goroutine.
// If maxWorkers is smaller or equal to zero, the pool will not limit the
// amount of workers.
//
// The passed errCollector is used to collect the errors returned by the tasks.
// For short-lived pools, the built-in [ToSlice] collector should be a good fit.
//
// The collector is called each time a task returns a non-nil error.
// If the [context.Context] given to [NewPool] is done or a [Task]
// returned cancel, the collector will be called with the result of calling
// [context.Context.Err] once.
// Should tasks return that same context error, it will not be included again
// to prevent duplication.
//
// If errCollector is nil, all errors will be discarded.
//
// The [context.Context] can be used to cancel the execution of running tasks.
// Since the context is internally wrapped to allow a [Task] to cancel other
// tasks, a [Task] should always use the context given to it as argument,
// instead of the context given to NewPool.
//
// Once a pool is created, a manager goroutine is started that distributes work
// among running workers, or starts new workers if maxWorkers allows it.
// If a task finishes and there is no other task queued up, the manager will
// stop the worker that finished the task, so that there are no idle workers.
//
// The manager goroutine will run always continue to run, and will only stop if
// the context given to NewPool is cancelled, [Pool.Last] is called, or
// [Pool.Wait] is called.
func NewPool(ctx context.Context, maxWorkers int, errCollector Collector[error]) *Pool {
	ctx, cancel := context.WithCancel(ctx)

	p := Pool{
		tasks:        make(chan Task),
		work:         make(chan Task),
		errChan:      make(chan error),
		kill:         make(chan struct{}),
		ctx:          ctx,
		cancel:       cancel,
		last:         make(chan struct{}),
		done:         make(chan struct{}),
		errCollector: errCollector,
	}

	p.startManager(maxWorkers)

	return &p
}

// Go adds the given [Task] to the queue to be executed by the worker pool, and
// returns true.
//
// It does not block.
//
// Tasks are executed in the order they are added in.
//
// If [Pool.Last], or [Pool.Wait] have been called, or if the context given to
// [NewPool] is done, then Go will discard t and return false.
//
// It is safe to concurrently call Go.
func (p *Pool) Go(t Task) (ok bool) {
	select {
	case <-p.ctx.Done():
		return false
	case <-p.last:
		return false
	default:
		p.tasks <- t
		return true
	}
}

// Wait calls [Pool.Last] and then blockingly waits until all tasks finish
// executing.
//
// It is safe to concurrently call Wait.
func (p *Pool) Wait() {
	p.Last()
	<-p.done
}

// Last signals the manager that the last task has just been added.
// After that last task finishes executing, the manager goroutine will exit
// leaving the pool unable to execute any new tasks.
//
// In contrast to [Pool.Wait], Last won't block until the tasks have finished.
//
// It is safe to concurrently call Last.
func (p *Pool) Last() {
	p.stopOnce.Do(func() {
		close(p.last)
	})
}

func (p *Pool) startManager(maxWorkers int) {
	go func() {
		var waiting list.List
		var runningWorkers int

	Loop:
		for {
			// Select will choose a case at random, if multiple cases
			// are ready at the same time.
			// Since a context cancellation/expiration should always take
			// precedence, check first if that is the case.
			select {
			case <-p.ctx.Done():
				p.handleErr(p.ctx.Err())
				break Loop
			case <-p.last:
				break Loop
			default:
			}

			select {
			case <-p.ctx.Done():
				p.handleErr(p.ctx.Err())
				break Loop
			case <-p.last:
				break Loop
			case task := <-p.tasks:
				if maxWorkers <= 0 || runningWorkers < maxWorkers {
					p.startWorker()
					runningWorkers++
					p.work <- task
				} else {
					waiting.PushBack(task)
				}
			case err := <-p.errChan:
				p.handleErr(err)

				if waiting.Len() == 0 {
					// we have no work waiting, so kill the idle goroutine
					p.kill <- struct{}{}
					runningWorkers--
				} else {
					p.work <- waiting.Front().Value.(Task)
					waiting.Remove(waiting.Front())
				}
			}
		}

		select {
		case <-p.ctx.Done():
			// if both done and last are ready, we might have exited the above
			// select for p.last and never captured the context cancellation
			p.handleErr(p.ctx.Err())
			// drain the task channel, so that Go never blocks
		Drain:
			for {
				select {
				case <-p.tasks:
				default:
					break Drain
				}
			}
		case <-p.last:
			// queue all work added before last was called
		QueueLast:
			for {
				select {
				case task := <-p.tasks:
					waiting.PushBack(task)
				default:
					break QueueLast
				}
			}

		ExecRemaining:
			for waiting.Len() > 0 {
				select {
				case <-p.ctx.Done():
					p.handleErr(p.ctx.Err())
					break ExecRemaining
				default:
				}

				task := waiting.Front().Value.(Task)
				waiting.Remove(waiting.Front())

				if maxWorkers <= 0 || runningWorkers < maxWorkers {
					p.startWorker()
					runningWorkers++
					p.work <- task
					continue
				}

				// maxWorkers is reached, wait for a worker to finish
				select {
				case <-p.ctx.Done():
					p.handleErr(p.ctx.Err())
					break ExecRemaining
				case err := <-p.errChan:
					p.handleErr(err)
					p.work <- task
				}
			}
		}

		// wait for all workers to exit
		for runningWorkers > 0 {
			err := <-p.errChan
			p.handleErr(err)
			p.kill <- struct{}{}
			runningWorkers--
		}

		// we're done, yay; close p.done to signal wait the wait is over
		close(p.done)
	}()
}

func (p *Pool) startWorker() {
	go func() {
		for {
			select {
			case <-p.kill:
				return
			case task := <-p.work:
				var err error
				var cancel bool

				func() {
					defer func() {
						if r := recover(); r != nil {
							err = &PanicError{r}
							if p.CancelOnPanic {
								cancel = true
							}
						}
					}()

					err, cancel = task(p.ctx)
				}()

				if cancel {
					p.cancel()
				}
				p.errChan <- err
			}
		}
	}()
}

func (p *Pool) handleErr(err error) {
	if err == nil {
		return
	}

	if p.errCollector == nil {
		return
	}

	if errors.Is(p.ctx.Err(), err) {
		if p.capturedContextErr {
			return
		}

		p.capturedContextErr = true
	}

	p.errCollector(err)
}

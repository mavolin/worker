package worker

import (
	"container/list"
	"context"
	"sync"
)

// ResultPool is the same as [ResultPool], but is able to collect result values.
//
// Refer to [NewResultPool] for an explanation of the pools inner workings.
//
// If you don't require collection of result values, use [ResultPool].
type ResultPool[T any] struct {
	tasks chan ResultTask[T] // Go is called and sends task to manager
	// if maxWorkers reached, task is queued, otherwise ->
	work chan ResultTask[T] // manger sends task to worker

	resChan chan Result[T] // worker finishes tasks and reports (nil) error to manager

	kill chan struct{} // manager receives err, if no tasks are queued -> kill the idle worker

	ctx      context.Context
	cancel   context.CancelFunc
	last     chan struct{} // closed by Last to indicate that no new tasks should be accepted
	stopOnce sync.Once

	done chan struct{} // closed by manager once ctx.Done() is closed and all workers have exited

	resCollector Collector[Result[T]]
	ctxErr       error

	// CancelOnPanic, if true, will cancel the context of all other workers
	// and last accepting new work, if a worker panics.
	//
	// If left false, the pool will continue to process new work.
	CancelOnPanic bool
}

// ResultTask is the function given to a [ResultPool].
//
// It's result and err are collected by the ResultPool and returned by
// [ResultPool.Wait].
//
// If ResultTask returns true for cancel, the pool will cancel the context given
// to all running Tasks.
// It is the same as calling the cancel func of the NewPool directly, however,
// the cancel bool was added, as it is more explicit and doesn't require
// access to the original's context cancel func.
type ResultTask[T any] func(context.Context) (result T, err error, cancel bool)

// Result stores the result and error returned by a [ResultTask].
type Result[T any] struct {
	Result T
	Err    error
}

// NewResultPool creates a new [ResultPool] that will run at most maxWorkers
// workers and additionally a single manager goroutine.
// If maxWorkers is smaller or equal to zero, the pool will not limit the
// amount of workers.
//
// The passed resCollector is used to collect the results returned by the tasks,
// and is called each time a task returns
// For most pools, the built-in [ToSlice] collector should be a good fit.
//
// If resCollector is nil, all results will be discarded.
//
// The [context.Context] can be used to cancel the execution of running tasks.
// Since the context is internally wrapped to allow a [ResultTask] to cancel
// other  tasks, a [ResultTask] should always use the context given to it as
// argument, instead of the context given to NewResultPool.
//
// Once a pool is created, a manager goroutine is started that distributes work
// among running workers, or starts new workers if maxWorkers allows it.
// If a task finishes and there is no other task queued up, the manager will
// stop the worker that finished the task, so that there are no idle workers.
//
// The manager goroutine will run always continue to run, until the context
// given to NewResultPool is cancelled or [ResultPool.Last] (or
// [ResultPool.Wait], which implicitly calls [ResultPool.Last]) is called.
func NewResultPool[T any](ctx context.Context, maxWorkers int, resCollector Collector[Result[T]]) *ResultPool[T] {
	ctx, cancel := context.WithCancel(ctx)

	p := ResultPool[T]{
		tasks:        make(chan ResultTask[T]),
		work:         make(chan ResultTask[T]),
		resChan:      make(chan Result[T]),
		kill:         make(chan struct{}),
		ctx:          ctx,
		cancel:       cancel,
		last:         make(chan struct{}),
		done:         make(chan struct{}),
		resCollector: resCollector,
	}

	p.startManager(maxWorkers)

	return &p
}

// Go adds the given [ResultTask] to the queue to be executed by the worker
// pool, and returns true.
//
// It does not block.
//
// Tasks are executed in the order they are added in.
//
// If [ResultPool.Last], or [ResultPool.Wait] have been called, or if the
// context given to [NewResultPool] is done, then Go will discard t and return
// false.
//
// It is safe to concurrently call Go.
func (p *ResultPool[T]) Go(t ResultTask[T]) (ok bool) {
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

// Wait calls [ResultPool.Last] and then blockingly waits until all tasks
// finish executing.
//
// It returns the error returned by [context.Context.Err], if the context given
// to NewResultPool is done or a task returned cancel.
//
// It is safe to concurrently call Wait.
func (p *ResultPool[T]) Wait() error {
	p.Last()
	<-p.done
	return p.ctxErr
}

// Last signals the manager that the last task has just been added.
// After that last task finishes executing, the manager goroutine will exit
// leaving the pool unable to execute any new tasks.
//
// In contrast to [ResultPool.Wait], Last won't block until the tasks have
// finished.
//
// It is safe to concurrently call Last.
func (p *ResultPool[T]) Last() {
	p.stopOnce.Do(func() {
		close(p.last)
	})
}

func (p *ResultPool[T]) startManager(maxWorkers int) {
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
				// Store this in a variable, because we might exit because of
				// Last.
				// If between then and the closure of done the context expires
				// and Wait were to return ctx.Err directly instead of ctxErr,
				// we might return an error that was not actually the cause of
				// the exit.
				p.ctxErr = p.ctx.Err()
				break Loop
			case <-p.last:
				break Loop
			default:
			}

			select {
			case <-p.ctx.Done():
				p.ctxErr = p.ctx.Err()
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
			case res := <-p.resChan:
				p.resCollector(res)

				if waiting.Len() == 0 {
					// we have no work waiting, so kill the idle goroutine
					p.kill <- struct{}{}
					runningWorkers--
				} else {
					p.work <- waiting.Front().Value.(ResultTask[T])
					waiting.Remove(waiting.Front())
				}
			}
		}

		select {
		case <-p.ctx.Done():
			// if both done and last are ready, we might have exited the above
			// select for p.last and never captured the context cancellation
			p.ctxErr = p.ctx.Err()
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
					p.ctxErr = p.ctx.Err()
					break ExecRemaining
				default:
				}

				task := waiting.Front().Value.(ResultTask[T])
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
					p.ctxErr = p.ctx.Err()
					break ExecRemaining
				case res := <-p.resChan:
					p.resCollector(res)
					p.work <- task
				}
			}
		}

		// wait for all workers to exit
		for runningWorkers > 0 {
			res := <-p.resChan
			p.resCollector(res)
			p.kill <- struct{}{}
			runningWorkers--
		}

		// we're done, yay; close p.done to signal wait the wait is over
		close(p.done)
	}()
}

func (p *ResultPool[T]) startWorker() {
	go func() {
		for {
			select {
			case <-p.kill:
				return
			case task := <-p.work:
				var res T
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

					res, err, cancel = task(p.ctx)
				}()

				if cancel {
					p.cancel()
				}
				p.resChan <- Result[T]{res, err}
			}
		}
	}()
}

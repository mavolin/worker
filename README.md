<div align="center">
<h1>worker</h1>

[![Test](https://github.com/mavolin/worker/actions/workflows/test.yml/badge.svg)](https://github.com/mavolin/worker/actions)
[![Code Coverage](https://codecov.io/gh/mavolin/worker/branch/develop/graph/badge.svg?token=ewFEQGgMES)](https://codecov.io/gh/mavolin/worker)
[![Go Report Card](https://goreportcard.com/badge/github.com/mavolin/worker)](https://goreportcard.com/report/github.com/mavolin/worker)
[![License MIT](https://img.shields.io/github/license/mavolin/worker)](https://github.com/mavolin/worker/blob/develop/LICENSE)
</div>

---

## About

`worker` is a small library that provides a concurrency limiter and a worker pool.

## Main Features

**`Limiter`**
> Limits the amount of times a task is executed at once.
> * ⤵ Tasks are blockingly executed in the same goroutine as the caller
> * 👮 Ensures has a task is only executed n-times at the same time
> * 🔄 Tasks are executed in the order they are added in
> * 🚚 Result collection and error handling is done by the caller
> * ⭐ Support for `context.Context`

**`Pool` and `ResultPool`**
> Execute multiple tasks at the same time.
> * ⤴ Task are executed in a separate goroutine, with max. n+1 goroutines running
> * 👮 Optionally limit the amount of concurrently running workers
> * 🔄 Tasks are executed in the order they are added in
> * 🚫 Tasks can abort execution of other tasks
> * 🚚 Result collection and error handling is done by the pool
> * ⭐ Support for `context.Context`
> * ⌚ `Wait()` to await the completion of all tasks

## Examples

First impressions matter, so here are examples of a limiter and a pool:

```go
var l = worker.NewLimiter(5)

// veryExpensiveComputation does a very expensive computation and returns with
// the result.
// At any given time only up to 5 computations will be made simultaneously.
// If there are more than 5 calls to veryExpensiveComputations at once, 
// veryExpensiveComputation will queue up computations until another is
// finished.
//
// If ctx is cancelled before the computation starts, veryExpensiveComputation
// will return with ctx.Err.
func veryExpensiveComputation(ctx context.Context) (res int, err error) {
    ran := l.DoContext(ctx, func() {
        // assign something to res and err
    })
    if !ran {
        return 0, ctx.Err()
    }
    return res, err
}
```

```go
// veryExpensiveComputation does a very expensive computation.
//
// It splits the computation into 123 smaller computations.
// These computations are executed concurrently, 5 at a time.
//
// If ctx is cancelled before the computation concludes, ctx.Err() is returned.
//
// Otherwise, the result or an error is returned.
func veryExpensiveComputation(ctx context.Context) (res int, err error) {
    partials := make([]worker.Result[int], 0, 123)
    p := worker.NewResultPool(ctx, 5, worker.ToSlice(&partials))
    
    for i := 0; i < 123; i++ {
        p.Go(func(ctx context.Context) (result int, err error, cancel bool) {
            // return the result of the computation
        })
    }
    
    if ctxErr := p.Wait(); ctxErr != nil {
        return 0, ctxErr
    }
	
    for _, partial := range partials {
        if partial.Err != nil {
            return 0, err
        }

        // compute res from partial result
    }
	
    return res, nil
}
```

## License

Built with ❤ by [Maximilian von Lindern](https://github.com/mavolin).
Available under the [MIT License](./LICENSE).

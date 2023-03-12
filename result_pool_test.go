package worker

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ExampleResultPool() {
	results := make([]Result[float64], 0, 50)
	p := NewResultPool(context.Background(), 5, ToSlice(&results))

	for i := 0; i < 50; i++ {
		p.Go(func(ctx context.Context) (result float64, err error, cancel bool) {
			return rand.Float64(), nil, false
		})
	}

	_ = p.Wait()
	for _, result := range results {
		fmt.Println(result.Result, result.Err)
	}
}

func ExampleResultPool_contextExpiration() {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	results := make([]Result[int], 0, 4)
	p := NewResultPool(ctx, 1, ToSlice(&results))

	for i := 0; i < 4; i++ {
		i := i
		p.Go(func(ctx context.Context) (result int, err error, cancel bool) {
			time.Sleep(26 * time.Millisecond)
			return i, nil, false
		})
	}

	ctxErr := p.Wait()
	if ctxErr != nil {
		fmt.Println(ctxErr)
	}
	for _, result := range results {
		fmt.Println(result.Result, result.Err)
	}

	// Output:
	// context deadline exceeded
	// 0 <nil>
	// 1 <nil>
}

func ExampleResultPool_cancellation() {
	results := make([]Result[int], 0, 10)
	p := NewResultPool(context.Background(), 1, ToSlice(&results))

	for i := 1; i <= 10; i++ {
		i := i
		p.Go(func(ctx context.Context) (result int, err error, cancel bool) {
			if i == 5 {
				return 5, errors.New("ew, five"), true
			}
			return i, nil, false
		})
	}

	ctxErr := p.Wait()
	if ctxErr != nil {
		fmt.Println(ctxErr)
	}
	for _, result := range results {
		fmt.Println(result.Result, result.Err)
	}

	// Output:
	// context canceled
	// 1 <nil>
	// 2 <nil>
	// 3 <nil>
	// 4 <nil>
	// 5 ew, five
}

// Quick note on these tests:
// Since there is no easy/proper way of testing Pool, these test rely on
// time.
// This, in itself, is of course a flawed concept of testing, however,
// some tests are better than no tests.
// Still, this means that if a test fails,it does not always mean that it
// actually failed, just that (little) enough time has passed to make
// the test report a failure.

func TestResultPool(t *testing.T) {
	t.Parallel()

	t.Run("maxWorkers", func(t *testing.T) {
		t.Parallel()

		t.Run("0", func(t *testing.T) {
			t.Parallel()

			var expectRuns int32 = 10_000
			var actualRuns atomic.Int32

			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			results := make([]Result[any], 0, expectRuns)
			p := NewResultPool(ctx, 0, ToSlice(&results))

			for i := 0; i < int(expectRuns); i++ {
				p.Go(func(ctx context.Context) (result any, err error, cancel bool) {
					actualRuns.Add(1)
					return nil, nil, false
				})
			}

			ctxErr := p.Wait()
			require.NoError(t, ctxErr)
			require.Len(t, results, int(expectRuns))
			assert.Equal(t, expectRuns, actualRuns.Load())
		})

		t.Run("1", func(t *testing.T) {
			t.Parallel()

			var actualRuns int

			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			var results []Result[any]
			p := NewResultPool(ctx, 1, ToSlice(&results))

			for i := 0; i < 3; i++ {
				p.Go(func(ctx context.Context) (result any, err error, cancel bool) {
					actualRuns++
					time.Sleep(50 * time.Millisecond)
					return nil, nil, false
				})
			}
			p.Last()

			for expectRuns := 1; expectRuns <= 3; expectRuns++ {
				assert.Equal(t, expectRuns, actualRuns)
				time.Sleep(55 * time.Millisecond)
			}

			ctxErr := p.Wait()
			require.NoError(t, ctxErr)
			require.Len(t, results, 3)
		})
	})

	t.Run("result collection", func(t *testing.T) {
		t.Parallel()

		expect := []Result[int]{{1, nil}, {2, nil}}

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		var actual []Result[int]
		p := NewResultPool(ctx, 1, ToSlice(&actual))

		p.Go(func(ctx context.Context) (result int, err error, cancel bool) {
			return expect[0].Result, expect[0].Err, false
		})
		p.Go(func(ctx context.Context) (result int, err error, cancel bool) {
			return expect[1].Result, expect[1].Err, false
		})

		ctxErr := p.Wait()
		assert.NoError(t, ctxErr)
		assert.Equal(t, expect, actual)
	})

	t.Run("cancellation", func(t *testing.T) {
		t.Parallel()

		t.Run("in task", func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			var results []Result[any]
			p := NewResultPool(ctx, 1, ToSlice(&results))

			p.Go(func(ctx context.Context) (result any, err error, cancel bool) {
				return nil, nil, true
			})

			var execed bool
			p.Go(func(ctx context.Context) (result any, err error, cancel bool) {
				execed = true
				return nil, nil, false
			})

			ctxErr := p.Wait()
			assert.Equal(t, context.Canceled, ctxErr)
			assert.Len(t, results, 1)

			assert.Falsef(t, execed, "second task should not have been executed")
		})

		t.Run("globally", func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			var results []Result[any]
			p := NewResultPool(ctx, 1, ToSlice(&results))

			p.Go(func(ctx context.Context) (any, error, bool) {
				cancel()
				return nil, nil, false
			})
			var execed bool
			p.Go(func(ctx context.Context) (result any, err error, cancel bool) {
				execed = true
				return nil, nil, false
			})

			ctxErr := p.Wait()
			assert.Equal(t, context.Canceled, ctxErr)
			assert.Len(t, results, 1)

			assert.Falsef(t, execed, "second task should not have been executed")
		})
	})

	t.Run("do discards tasks after last", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		var results []Result[any]
		p := NewResultPool(ctx, 1, ToSlice(&results))

		p.Go(func(ctx context.Context) (any, error, bool) {
			p.Last()
			return nil, nil, false
		})
		var execed bool
		p.Go(func(ctx context.Context) (res any, err error, cancel bool) {
			execed = true
			return nil, nil, false
		})

		ctxErr := p.Wait()
		assert.NoError(t, ctxErr)
		assert.Len(t, results, 1)

		assert.Falsef(t, execed, "second task should not have been executed")
	})
}

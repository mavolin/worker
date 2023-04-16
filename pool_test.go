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

func ExamplePool() {
	var errs []error
	p := NewPool(context.Background(), 5, ToSlice(&errs))

	for i := 0; i < 50; i++ {
		p.Go(func(ctx context.Context) (err error, _ bool) {
			if rand.Float64() > 0.8 {
				return errors.New("you're unlucky"), false
			}

			return nil, false
		})
	}

	p.Wait()
	if len(errs) > 0 {
		panic(errors.Join(errs...))
	}
}

func ExamplePool_contextExpiration() {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	var errs []error
	p := NewPool(ctx, 1, ToSlice(&errs))

	for i := 0; i < 4; i++ {
		i := i
		p.Go(func(ctx context.Context) (err error, cancel bool) {
			fmt.Println(i)
			time.Sleep(26 * time.Millisecond)
			return nil, false
		})
	}

	p.Wait()
	if len(errs) > 0 {
		fmt.Println(errors.Join(errs...))
	}

	// Output:
	// 0
	// 1
	// context deadline exceeded
}

func ExamplePool_cancellation() {
	var errs []error
	p := NewPool(context.Background(), 1, ToSlice(&errs))

	for i := 1; i <= 10; i++ {
		i := i
		p.Go(func(ctx context.Context) (err error, cancel bool) {
			fmt.Println(i)

			if i == 5 {
				return errors.New("ew, five"), true
			}
			return nil, false
		})
	}

	p.Wait()
	if len(errs) > 0 {
		fmt.Println(errors.Join(errs...))
	}

	// Output:
	// 1
	// 2
	// 3
	// 4
	// 5
	// context canceled
	// ew, five
}

// Quick note on these tests:
// Since there is no easy/proper way of testing Pool, these test rely on
// time.
// This, in itself, is of course a flawed concept of testing, however,
// some tests are better than no tests.
// Still, this means that if a test fails,it does not always mean that it
// actually failed, just that (little) enough time has passed to make
// the test report a failure.

func TestPool(t *testing.T) {
	t.Parallel()

	t.Run("maxWorkers", func(t *testing.T) {
		t.Parallel()

		t.Run("0", func(t *testing.T) {
			t.Parallel()

			var expectRuns int32 = 10_000
			var actualRuns atomic.Int32

			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			var errs []error
			p := NewPool(ctx, 0, ToSlice(&errs))

			for i := 0; i < int(expectRuns); i++ {
				p.Go(func(ctx context.Context) (err error, cancel bool) {
					actualRuns.Add(1)
					return nil, false
				})
			}

			p.Wait()
			require.Empty(t, errs)
			assert.Equal(t, expectRuns, actualRuns.Load())
		})

		t.Run("1", func(t *testing.T) {
			t.Parallel()

			var actualRuns atomic.Int32

			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			var errs []error
			p := NewPool(ctx, 1, ToSlice(&errs))

			for i := 0; i < 3; i++ {
				p.Go(func(ctx context.Context) (err error, cancel bool) {
					actualRuns.Add(1)
					time.Sleep(50 * time.Millisecond)
					return nil, false
				})
			}
			p.Last()

			for expectRuns := int32(1); expectRuns <= 3; expectRuns++ {
				assert.Equal(t, expectRuns, actualRuns.Load())
				time.Sleep(55 * time.Millisecond)
			}

			p.Wait()
			require.Empty(t, errs)
		})
	})

	t.Run("error collection", func(t *testing.T) {
		t.Parallel()

		expect := []error{errors.New("foo")}

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		var actual []error
		p := NewPool(ctx, 0, ToSlice(&actual))

		p.Go(func(ctx context.Context) (err error, cancel bool) {
			return expect[0], false
		})
		p.Go(func(ctx context.Context) (err error, cancel bool) {
			return nil, false
		})

		p.Wait()
		assert.Equal(t, expect, actual)
	})

	t.Run("cancellation", func(t *testing.T) {
		t.Parallel()

		t.Run("in task", func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			var errs []error
			p := NewPool(ctx, 1, ToSlice(&errs))

			p.Go(func(ctx context.Context) (err error, cancel bool) {
				return nil, true
			})

			var execed bool
			p.Go(func(ctx context.Context) (err error, cancel bool) {
				execed = true
				return nil, false
			})

			p.Wait()
			assert.Equal(t, []error{context.Canceled}, errs)

			assert.Falsef(t, execed, "second task should not have been executed")
		})

		t.Run("globally", func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			var errs []error
			p := NewPool(ctx, 1, ToSlice(&errs))

			p.Go(func(ctx context.Context) (error, bool) {
				cancel()
				return nil, false
			})
			var execed bool
			p.Go(func(ctx context.Context) (err error, cancel bool) {
				execed = true
				return nil, false
			})

			p.Wait()
			assert.Equal(t, []error{context.Canceled}, errs)

			assert.Falsef(t, execed, "second task should not have been executed")
		})
	})

	t.Run("do discards tasks after last", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		var errs []error
		p := NewPool(ctx, 0, ToSlice(&errs))

		p.Go(func(ctx context.Context) (error, bool) {
			p.Last()
			return nil, false
		})
		var execed bool
		p.Go(func(ctx context.Context) (err error, cancel bool) {
			execed = true
			return nil, false
		})

		p.Wait()
		require.Empty(t, errs)

		assert.Falsef(t, execed, "second task should not have been executed")
	})
}

package worker

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLimiter_Do(t *testing.T) {
	t.Parallel()

	l := NewLimiter(5)

	expect50 := int32(5)
	expect150 := int32(10)
	var actual atomic.Int32

	for i := 0; i < int(expect150); i++ {
		go func() {
			l.Do(func() {
				actual.Add(1)
				time.Sleep(100 * time.Millisecond)
			})
		}()
	}

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, expect50, actual.Load())

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, expect150, actual.Load())
}

func TestLimiter_Go(t *testing.T) {
	t.Parallel()

	l := NewLimiter(5)

	expect50 := int32(5)
	expect150 := int32(10)
	var actual atomic.Int32

	for i := 0; i < int(expect150); i++ {
		l.Go(func() {
			actual.Add(1)
			time.Sleep(100 * time.Millisecond)
		})
	}

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, expect50, actual.Load())

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, expect150, actual.Load())
}

func TestLimiter_Try(t *testing.T) {
	t.Parallel()

	l := NewLimiter(5)

	expect50 := int32(5)
	expect150 := int32(5)
	var actual atomic.Int32

	for i := 0; i < int(expect150); i++ {
		ran := l.Try(func() {
			actual.Add(1)
			time.Sleep(100 * time.Millisecond)
		})
		assert.True(t, ran)
	}

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, expect50, actual.Load())

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, expect150, actual.Load())
}

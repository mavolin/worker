package worker

import "fmt"

// PanicError is the error used to capture panics in workers.
type PanicError struct {
	Recover any
}

var _ error = (*PanicError)(nil)

func (err *PanicError) Error() string {
	return fmt.Sprintf("panic: %v", err.Recover)
}

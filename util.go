package worker

// Collector is a function used to collect errors or results from a [Pool] or
// [ResultPool].
//
// It need not be concurrent safe, and should never block.
type Collector[T any] func(T)

// ToSlice returns a new collector that collects the errors/results of the
// [Pool]/[ResultPool] in the passed slice.
//
// The slice must only be accessed after pool.Wait returns.
func ToSlice[T any](ts *[]T) Collector[T] {
	return func(t T) {
		*ts = append(*ts, t)
	}
}

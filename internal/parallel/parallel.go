package parallel

// Result is a generic wrapper for task outputs
type Result[R any] struct {
	Index int
	Value *R
	Error error
}

// FirstSuccessful executes f(p) for each p in params in parallel.
// It returns the first successful result based on the original order of params,
// and the list of errors which happened before first success.
func FirstSuccessful[P any, R any](
	params []P,
	f func(*P) (*R, error),
) (*R, []error) {
	n := len(params)
	resChan := make(chan Result[R], n)

	for i := range n {
		go func(index int, param *P) {
			val, err := f(param)
			resChan <- Result[R]{Index: index, Value: val, Error: err}
		}(i, &params[i])
	}

	pending := make([]*Result[R], n)
	nextToReturn := 0

	for range n {
		res := <-resChan
		pending[res.Index] = &res

		for nextToReturn < n && pending[nextToReturn] != nil {
			if pending[nextToReturn].Error == nil {
				previousErrs := make([]error, nextToReturn)
				for i := range nextToReturn {
					previousErrs[i] = pending[i].Error
				}
				return pending[nextToReturn].Value, previousErrs
			}
			nextToReturn++
		}
	}

	errs := make([]error, n)
	for i := range n {
		errs[i] = pending[i].Error
	}

	return nil, errs
}

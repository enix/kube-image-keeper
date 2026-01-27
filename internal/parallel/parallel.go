package parallel

// Result is a generic wrapper for task outputs
type Result[R any] struct {
	Index   int
	Value   *R
	Success bool
}

// FirstSuccessful executes f(p) for each p in params in parallel.
// It returns the first successful result based on the original order of params.
func FirstSuccessful[P any, R any](
	params []P,
	f func(*P) (*R, bool),
) *R {
	n := len(params)
	resChan := make(chan Result[R], n)

	for i := range n {
		go func(index int, param *P) {
			val, success := f(param)
			resChan <- Result[R]{Index: index, Value: val, Success: success}
		}(i, &params[i])
	}

	pending := make([]*Result[R], n)
	nextToReturn := 0

	for range n {
		res := <-resChan
		pending[res.Index] = &res

		for nextToReturn < n && pending[nextToReturn] != nil {
			if pending[nextToReturn].Success {
				return pending[nextToReturn].Value
			}
			nextToReturn++
		}
	}

	return nil
}

package tree

import "context"

type Handler[C any, D any, R any] interface {
	Apply(ctx context.Context, command C, dynamic D) (R, error)
}

func Route[C any, D any, R any](ctx context.Context, next Handler[C, D, R], command C, dynamic D) (R, error) {
	if next == nil {
		return zero[R](), nil
	}
	return next.Apply(ctx, command, dynamic)
}

func zero[T any]() T {
	var value T
	return value
}

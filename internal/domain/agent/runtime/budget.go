// Package runtime defines request-scoped execution budgets shared by all agents.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

var (
	ErrBusy   = errors.New("drawing runtime is busy")
	ErrBudget = errors.New("model call budget exhausted")
	ErrSize   = errors.New("runtime size limit exceeded")
)

type Options struct {
	RequestTimeout  time.Duration `yaml:"request-timeout"`
	ModelTimeout    time.Duration `yaml:"model-timeout"`
	ToolTimeout     time.Duration `yaml:"tool-timeout"`
	MaxModelCalls   int           `yaml:"max-model-calls"`
	MaxRequestBytes int64         `yaml:"max-request-bytes"`
	MaxModelBytes   int64         `yaml:"max-model-bytes"`
	MaxToolBytes    int64         `yaml:"max-tool-bytes"`
}

func Defaults() Options {
	return Options{
		RequestTimeout: 180 * time.Second, ModelTimeout: 60 * time.Second, ToolTimeout: 20 * time.Second,
		MaxModelCalls: 8, MaxRequestBytes: 2 << 20, MaxModelBytes: 1 << 20, MaxToolBytes: 256 << 10,
	}
}
func (o Options) Validate() error {
	if o.RequestTimeout <= 0 || o.ModelTimeout <= 0 || o.ToolTimeout <= 0 || o.MaxModelCalls <= 0 || o.MaxRequestBytes <= 0 || o.MaxModelBytes <= 0 || o.MaxToolBytes <= 0 {
		return fmt.Errorf("runtime limits must all be positive")
	}
	return nil
}

type state struct {
	options Options
	mu      sync.Mutex
	calls   int
}
type key struct{}

func NewContext(ctx context.Context, o Options) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, o.RequestTimeout)
	return context.WithValue(ctx, key{}, &state{options: o}), cancel
}
func OptionsFrom(ctx context.Context) Options {
	if s, ok := ctx.Value(key{}).(*state); ok {
		return s.options
	}
	return Defaults()
}
func ModelContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if s, ok := ctx.Value(key{}).(*state); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.calls >= s.options.MaxModelCalls {
			return nil, nil, ErrBudget
		}
		s.calls++
	}
	c, cancel := context.WithTimeout(ctx, OptionsFrom(ctx).ModelTimeout)
	return c, cancel, nil
}

// ReadBounded reads at most limit+1 bytes to detect an oversized upstream body.
func ReadBounded(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, ErrSize
	}
	return b, nil
}

// EnsureContext supplies defaults for direct Runner callers without replacing a shared budget.
func EnsureContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Value(key{}).(*state); ok {
		return ctx, func() {}
	}
	return NewContext(ctx, Defaults())
}

package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSharedBudget(t *testing.T) {
	o := Defaults()
	o.MaxModelCalls = 2
	ctx, cancel := NewContext(context.Background(), o)
	defer cancel()
	for i := 0; i < 2; i++ {
		_, stop, err := ModelContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		stop()
	}
	if _, _, err := ModelContext(ctx); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
}
func TestParentDeadline(t *testing.T) {
	o := Defaults()
	o.RequestTimeout = time.Millisecond
	ctx, cancel := NewContext(context.Background(), o)
	defer cancel()
	child, stop, err := ModelContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	select {
	case <-child.Done():
		if !errors.Is(child.Err(), context.DeadlineExceeded) {
			t.Fatal(child.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("deadline lost")
	}
}
func TestReadBounded(t *testing.T) {
	for _, n := range []int{3, 4, 5} {
		b, err := ReadBounded(strings.NewReader(strings.Repeat("a", n)), 4)
		if n > 4 {
			if !errors.Is(err, ErrSize) {
				t.Fatal(err)
			}
		} else if err != nil || len(b) != n {
			t.Fatalf("%d: %q %v", n, b, err)
		}
	}
}
func TestOptions(t *testing.T) {
	o := Defaults()
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	o.MaxModelCalls = 0
	if o.Validate() == nil {
		t.Fatal("accepted zero budget")
	}
}

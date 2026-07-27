package marketdata

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheCoalescesConcurrentFetches(t *testing.T) {
	var cache Cache[int]
	var calls atomic.Int32
	start := make(chan struct{})
	fetch := func(context.Context) (int, error) {
		calls.Add(1)
		<-start
		return 42, nil
	}

	var wg sync.WaitGroup
	results := make([]int, 5)
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			value, err := cache.Get(context.Background(), time.Minute, fetch)
			if err != nil {
				t.Errorf("Get: %v", err)
			}
			results[index] = value
		}(i)
	}
	time.Sleep(20 * time.Millisecond)
	close(start)
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("fetch calls = %d, want 1", calls.Load())
	}
	for _, value := range results {
		if value != 42 {
			t.Fatalf("cached value = %d, want 42", value)
		}
	}
}

package config

import (
	"sync"
	"testing"
)

// TestReplaceWithConcurrentAccess exercises ReplaceWith against concurrent
// readers. It must run cleanly under -race; copying the embedded mutex (the
// original bug) would corrupt lock state here.
func TestReplaceWithConcurrentAccess(t *testing.T) {
	c := DefaultConfig()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = c.GetPort()
				_ = c.GetEndpoints()
				_ = c.GetLogLevel()
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				src := DefaultConfig()
				src.Port = 4000 + n
				c.ReplaceWith(src)
			}
		}(i)
	}
	wg.Wait()

	if c.GetPort() < 4000 {
		t.Fatalf("unexpected port after concurrent replace: %d", c.GetPort())
	}
}

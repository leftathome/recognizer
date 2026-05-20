package lock

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcquire_Succeeds(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".lock")
	lk, err := Acquire(p)
	if err != nil {
		t.Fatal(err)
	}
	lk.Release()
}

func TestAcquire_SecondConcurrentFails(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".lock")
	lk1, err := Acquire(p)
	if err != nil {
		t.Fatal(err)
	}
	defer lk1.Release()
	if _, err := Acquire(p); err == nil {
		t.Fatal("expected second Acquire to fail")
	}
}

func TestAcquire_AfterReleaseSucceeds(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".lock")
	lk1, _ := Acquire(p)
	lk1.Release()
	lk2, err := Acquire(p)
	if err != nil {
		t.Fatal(err)
	}
	lk2.Release()
}

func TestAcquire_MutualExclusion(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".lock")
	var inFlight, maxSeen int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for try := 0; try < 5; try++ {
				lk, err := Acquire(p)
				if err != nil {
					time.Sleep(time.Millisecond)
					continue
				}
				cur := atomic.AddInt32(&inFlight, 1)
				for {
					old := atomic.LoadInt32(&maxSeen)
					if cur <= old || atomic.CompareAndSwapInt32(&maxSeen, old, cur) {
						break
					}
				}
				time.Sleep(2 * time.Millisecond)
				atomic.AddInt32(&inFlight, -1)
				lk.Release()
				break
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&maxSeen); got != 1 {
		t.Errorf("mutual exclusion broken: maxSeen=%d, want 1", got)
	}
}

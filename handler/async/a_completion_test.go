package async

import (
	"testing"
	"time"
)

func TestMarkCompleteCompactsOutOfOrderAndDuplicateCompletions(t *testing.T) {
	runtime := &runtime{
		completed: make(map[uint64]struct{}),
		progress:  make(chan struct{}),
	}

	runtime.markComplete(2)
	completed := make(chan struct{})
	go func() {
		runtime.markComplete(1)
		close(completed)
	}()

	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("contiguous completion compaction did not terminate")
	}
	if runtime.watermark != 2 || len(runtime.completed) != 0 {
		t.Fatalf(
			"compacted state = watermark %d, pending %v; want watermark 2 and no pending completions",
			runtime.watermark,
			runtime.completed,
		)
	}

	runtime.markComplete(2)
	if len(runtime.completed) != 0 {
		t.Fatalf("duplicate completion retained pending state: %v", runtime.completed)
	}
}

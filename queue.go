package scout

import (
	"log"
	"sync"
)

// queueBuffer sizes the in-flight job channel.
const queueBuffer = 4096

type queueJob struct {
	name string
	fn   func() error
}

// Queue is a process-local job queue used to offload index writes.
//
// ponytail: in-process queue, not Redis. A buffered channel plus goroutine
// workers; jobs that cannot be enqueued (disabled, no workers started, buffer
// full) run synchronously with a warning, mirroring scout's
// "redis-queue not installed; falling back to synchronous indexing". Swap in a
// real broker here if jobs must survive a restart or fan out across hosts.
type Queue struct {
	enabled bool
	mu      sync.Mutex
	jobs    chan queueJob
	started bool
	closed  bool
	wg      sync.WaitGroup
}

// NewQueue returns a Queue. When enabled is false, Push always runs inline.
func NewQueue(enabled bool) *Queue {
	return &Queue{enabled: enabled, jobs: make(chan queueJob, queueBuffer)}
}

// Enabled reports whether dispatch is turned on.
func (q *Queue) Enabled() bool { return q.enabled }

// Start spawns workers that drain queued jobs. Calling it again is a no-op.
func (q *Queue) Start(workers int) {
	if workers <= 0 {
		workers = 1
	}
	q.mu.Lock()
	if q.started || q.closed {
		q.mu.Unlock()
		return
	}
	q.started = true
	q.wg.Add(workers)
	q.mu.Unlock()
	for i := 0; i < workers; i++ {
		go q.run()
	}
}

// Stop closes the queue and waits for in-flight jobs to finish. Pushes after
// Stop run inline.
func (q *Queue) Stop() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	close(q.jobs)
	// Release before Wait: a job may call Push, which needs mu.
	q.mu.Unlock()
	q.wg.Wait()
}

func (q *Queue) run() {
	defer q.wg.Done()
	for j := range q.jobs {
		if err := j.fn(); err != nil {
			log.Printf("scout: %s job failed: %v", jobLabel(j.name), err)
		}
	}
}

// Push dispatches fn to the queue, or runs it inline. Inline execution returns
// the job's error; enqueued jobs never fail the call.
func (q *Queue) Push(fn func() error) error { return q.PushNamed("", fn) }

// PushNamed dispatches fn under a queue name used in log messages, or runs it
// inline when the queue cannot accept it.
func (q *Queue) PushNamed(name string, fn func() error) error {
	if fn == nil {
		return nil
	}
	if !q.enabled {
		return runInline(name, fn)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.started || q.closed {
		log.Printf("scout: warning: queue has no workers; falling back to synchronous %s.", jobLabel(name))
		return runInline(name, fn)
	}
	select {
	case q.jobs <- queueJob{name: name, fn: fn}:
		return nil
	default:
		log.Printf("scout: warning: queue full; falling back to synchronous %s.", jobLabel(name))
		return runInline(name, fn)
	}
}

// runInline executes fn in the caller's goroutine.
func runInline(name string, fn func() error) error {
	if err := fn(); err != nil {
		log.Printf("scout: %s job failed: %v", jobLabel(name), err)
		return err
	}
	return nil
}

func jobLabel(name string) string {
	if name == "" {
		return "job"
	}
	return name
}

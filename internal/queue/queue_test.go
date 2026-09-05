package queue

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEnqueueAssignsIDAndState(t *testing.T) {
	q := New()
	id := q.Enqueue(Job{Type: JobVerify, Slug: "demo"})
	if id == "" {
		t.Fatal("Enqueue returned an empty id")
	}
	job, ok := q.Get(id)
	if !ok {
		t.Fatalf("Get(%q) = not found, want the enqueued job", id)
	}
	if job.State != StateQueued {
		t.Errorf("State = %q, want queued", job.State)
	}
	if job.Type != JobVerify || job.Slug != "demo" {
		t.Errorf("job = %+v, want a verify job for demo", job)
	}
	if job.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set on enqueue")
	}
}

func TestEnqueueIDsAreUnique(t *testing.T) {
	q := New()
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := q.Enqueue(Job{Type: JobAsk, Slug: "demo"})
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestClaimReturnsQueuedJobFIFO(t *testing.T) {
	q := New()
	id1 := q.Enqueue(Job{Type: JobVerify, Slug: "a"})
	id2 := q.Enqueue(Job{Type: JobVerify, Slug: "b"})

	ctx := context.Background()
	got1, ok := q.Claim(ctx)
	if !ok || got1.ID != id1 {
		t.Fatalf("first Claim = %+v ok=%v, want id %q", got1, ok, id1)
	}
	if got1.State != StateClaimed {
		t.Errorf("claimed job State = %q, want claimed", got1.State)
	}
	got2, ok := q.Claim(ctx)
	if !ok || got2.ID != id2 {
		t.Fatalf("second Claim = %+v ok=%v, want id %q", got2, ok, id2)
	}
}

func TestClaimBlocksUntilEnqueue(t *testing.T) {
	q := New()
	done := make(chan Job, 1)
	go func() {
		job, ok := q.Claim(context.Background())
		if ok {
			done <- job
		}
	}()

	// Give the claimer a moment to block on the empty queue.
	select {
	case <-done:
		t.Fatal("Claim returned before any job was enqueued")
	case <-time.After(20 * time.Millisecond):
	}

	id := q.Enqueue(Job{Type: JobExtend, Slug: "later"})
	select {
	case job := <-done:
		if job.ID != id {
			t.Errorf("Claim returned %q, want the just-enqueued %q", job.ID, id)
		}
	case <-time.After(time.Second):
		t.Fatal("Claim did not wake up after Enqueue")
	}
}

func TestClaimRespectsContextTimeout(t *testing.T) {
	q := New()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, ok := q.Claim(ctx)
	if ok {
		t.Fatal("Claim should return ok=false when the context expires with no job")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Claim blocked %v after ctx timeout, want a prompt return", elapsed)
	}
}

func TestDoneMarksJobDone(t *testing.T) {
	q := New()
	id := q.Enqueue(Job{Type: JobVerify, Slug: "demo"})
	if _, ok := q.Claim(context.Background()); !ok {
		t.Fatal("Claim failed")
	}
	q.Done(id)
	job, ok := q.Get(id)
	if !ok {
		t.Fatal("Get after Done should still find the job")
	}
	if job.State != StateDone {
		t.Errorf("State = %q, want done", job.State)
	}
}

func TestSetAnswer(t *testing.T) {
	q := New()
	id := q.Enqueue(Job{Type: JobAsk, Slug: "demo", Part: "part-01.md", Question: "why?"})
	q.SetAnswer(id, "because.")
	job, ok := q.Get(id)
	if !ok {
		t.Fatal("Get failed")
	}
	if job.Answer != "because." {
		t.Errorf("Answer = %q, want %q", job.Answer, "because.")
	}
}

func TestReclaimRequeuesStaleClaim(t *testing.T) {
	q := New()
	now := time.Unix(1000, 0)
	q.now = func() time.Time { return now }
	q.ReclaimAfter = time.Minute

	id := q.Enqueue(Job{Type: JobVerify, Slug: "demo"})
	if _, ok := q.Claim(context.Background()); !ok {
		t.Fatal("first Claim failed")
	}

	// Advance past the reclaim window: the stale claim should be re-queued and
	// handed out again by the next Claim.
	now = now.Add(2 * time.Minute)
	got, ok := q.Claim(context.Background())
	if !ok {
		t.Fatal("expected the stale claim to be reclaimable")
	}
	if got.ID != id {
		t.Errorf("reclaimed id = %q, want %q", got.ID, id)
	}
}

func TestReclaimLeavesFreshClaimAlone(t *testing.T) {
	q := New()
	now := time.Unix(1000, 0)
	q.now = func() time.Time { return now }
	q.ReclaimAfter = time.Minute

	q.Enqueue(Job{Type: JobVerify, Slug: "demo"})
	if _, ok := q.Claim(context.Background()); !ok {
		t.Fatal("first Claim failed")
	}

	// Within the reclaim window, a second claim should find nothing.
	now = now.Add(30 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, ok := q.Claim(ctx); ok {
		t.Error("a fresh claim should not be reclaimed")
	}
}

func TestWorkerPresence(t *testing.T) {
	q := New()
	now := time.Unix(1000, 0)
	q.now = func() time.Time { return now }
	q.PresenceWindow = time.Minute

	if q.WorkerConnected() {
		t.Error("no worker has been seen yet; WorkerConnected should be false")
	}
	q.MarkWorkerSeen()
	if !q.WorkerConnected() {
		t.Error("WorkerConnected should be true right after MarkWorkerSeen")
	}
	now = now.Add(2 * time.Minute)
	if q.WorkerConnected() {
		t.Error("WorkerConnected should be false once the presence window lapses")
	}
}

func TestConcurrentEnqueueClaim(t *testing.T) {
	q := New()
	const n = 50
	var wg sync.WaitGroup
	claimed := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, ok := q.Claim(context.Background())
			if ok {
				claimed <- job.ID
			}
		}()
	}
	for i := 0; i < n; i++ {
		q.Enqueue(Job{Type: JobVerify, Slug: "demo"})
	}
	wg.Wait()
	close(claimed)
	seen := map[string]bool{}
	for id := range claimed {
		if seen[id] {
			t.Fatalf("job %q was claimed by two workers", id)
		}
		seen[id] = true
	}
	if len(seen) != n {
		t.Errorf("claimed %d jobs, want %d", len(seen), n)
	}
}

// A correct job carries two payloads, not one: the excerpt the reader selected
// and the note saying what's wrong. Both must survive enqueue → claim intact,
// because the worker needs them to locate and apply the edit.
func TestCorrectJobRoundTrip(t *testing.T) {
	q := New()
	id := q.Enqueue(Job{
		Type:    JobCorrect,
		Slug:    "digital-synth-zig",
		Part:    "part-02.md",
		Excerpt: "the ring buffer is 512 samples",
		Note:    "it's 1024, see the code above",
	})

	job, ok := q.Claim(context.Background())
	if !ok {
		t.Fatal("Claim returned no job")
	}
	if job.ID != id {
		t.Errorf("ID = %q, want %q", job.ID, id)
	}
	if job.Type != JobCorrect {
		t.Errorf("Type = %q, want %q", job.Type, JobCorrect)
	}
	if job.Excerpt != "the ring buffer is 512 samples" {
		t.Errorf("Excerpt = %q, want the selected text", job.Excerpt)
	}
	if job.Note != "it's 1024, see the code above" {
		t.Errorf("Note = %q, want the reader's note", job.Note)
	}
	if job.State != StateClaimed {
		t.Errorf("State = %q, want %q", job.State, StateClaimed)
	}
}

// The worker parses `lathe work next` output as JSON, so the wire names are part
// of the contract with every installed copy of the /lathe-work skill.
func TestCorrectJobJSONFieldNames(t *testing.T) {
	raw, err := json.Marshal(Job{Type: JobCorrect, Excerpt: "x", Note: "y"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"correct"`, `"excerpt":"x"`, `"note":"y"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("marshalled job = %s, want it to contain %s", raw, want)
		}
	}
	// omitempty: an ask/verify/extend job must not grow empty correction fields.
	raw, err = json.Marshal(Job{Type: JobAsk})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "excerpt") || strings.Contains(string(raw), "note") {
		t.Errorf("ask job = %s, want no excerpt/note keys", raw)
	}
}

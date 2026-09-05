package serve_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/serve"
	"github.com/devenjarvis/lathe/internal/store"
)

// markWorkerConnected fires a short-lived GET /-/work so the server records
// worker presence (handleWorkNext marks it seen before it blocks). The request's
// context cancels almost immediately, so Claim returns "no task" (204) without
// blocking the test for the full long-poll window.
func markWorkerConnected(t *testing.T, srv *serve.Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/-/work", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("priming GET /-/work = %d, want 204 (no task)", w.Code)
	}
}

// claimNext fires GET /-/work and decodes the claimed job. A job must already be
// queued so the long-poll returns promptly.
func claimNext(t *testing.T, srv *serve.Server) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/-/work", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /-/work = %d, want 200 with a job", w.Code)
	}
	var job map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatalf("decode claimed job: %v (body=%q)", err, w.Body.String())
	}
	return job
}

func TestWorkerEndpointReportsConnection(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)

	req := httptest.NewRequest(http.MethodGet, "/-/worker", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	var resp struct {
		Connected bool `json:"connected"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Connected {
		t.Error("no worker has polled; /-/worker should report connected=false")
	}

	markWorkerConnected(t, srv)

	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/-/worker", nil))
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Connected {
		t.Error("a worker just polled; /-/worker should report connected=true")
	}
}

func TestWorkNextReturnsNoContentWhenIdle(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)
	// markWorkerConnected itself asserts the 204 idle response.
	markWorkerConnected(t, srv)
}

func TestVerifyEnqueuesWhenWorkerConnected(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusUnverified, []string{"part-01.md"})
	srv := serve.NewServer(dir)
	markWorkerConnected(t, srv)

	w := postVerify(t, srv, "test-tut")
	if w.Code != http.StatusOK {
		t.Fatalf("verify = %d, want 200", w.Code)
	}
	var resp struct {
		Mode  string `json:"mode"`
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Mode != "queued" || resp.JobID == "" {
		t.Fatalf("response = %+v, want mode=queued with a jobId", resp)
	}

	job := claimNext(t, srv)
	if job["type"] != "verify" || job["slug"] != "test-tut" {
		t.Errorf("claimed job = %v, want a verify job for test-tut", job)
	}
}

func TestVerifyHandsOffWhenNoWorker(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusUnverified, []string{"part-01.md"})
	srv := serve.NewServer(dir)

	w := postVerify(t, srv, "test-tut")
	if w.Code != http.StatusOK {
		t.Fatalf("verify = %d, want 200", w.Code)
	}
	var resp struct {
		Mode    string `json:"mode"`
		Command string `json:"command"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Mode != "handoff" || resp.Command != "/lathe-verify test-tut" {
		t.Errorf("response = %+v, want the handoff command", resp)
	}
}

func TestExtendEnqueuesWithGuidance(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusVerified, []string{"part-01.md"})
	srv := serve.NewServer(dir)
	markWorkerConnected(t, srv)

	w := postExtend(t, srv, "test-tut", []byte(`{"guidance":"cover errors\n  next"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("extend = %d, want 200", w.Code)
	}
	var resp struct {
		Mode  string `json:"mode"`
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Mode != "queued" {
		t.Fatalf("response = %+v, want mode=queued", resp)
	}

	job := claimNext(t, srv)
	if job["type"] != "extend" || job["guidance"] != "cover errors next" {
		t.Errorf("claimed job = %v, want extend with collapsed guidance", job)
	}
}

func TestAskRoundTripsAnswerThroughQueue(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusVerified, []string{"part-01.md"})
	srv := serve.NewServer(dir)
	markWorkerConnected(t, srv)

	// Browser asks → enqueued.
	askReq := httptest.NewRequest(http.MethodPost, "/-/ask/test-tut/part-01.md",
		bytes.NewReader([]byte(`{"question":"what is a foo?"}`)))
	askReq.Header.Set("Content-Type", "application/json")
	askW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(askW, askReq)
	if askW.Code != http.StatusOK {
		t.Fatalf("ask = %d, want 200", askW.Code)
	}
	var enq struct {
		Mode  string `json:"mode"`
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(askW.Body.Bytes(), &enq); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if enq.Mode != "queued" || enq.JobID == "" {
		t.Fatalf("ask response = %+v, want queued with jobId", enq)
	}

	// Worker claims it.
	job := claimNext(t, srv)
	if job["type"] != "ask" || job["question"] != "what is a foo?" || job["part"] != "part-01.md" {
		t.Fatalf("claimed ask job = %v", job)
	}

	// Worker reports the answer.
	ansReq := httptest.NewRequest(http.MethodPost, "/-/work/"+enq.JobID+"/answer",
		bytes.NewReader([]byte(`{"answer":"A **foo** is a thing."}`)))
	ansReq.Header.Set("Content-Type", "application/json")
	ansW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(ansW, ansReq)
	if ansW.Code != http.StatusNoContent {
		t.Fatalf("answer = %d, want 204", ansW.Code)
	}

	// Browser polls the answer.
	getW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getW, httptest.NewRequest(http.MethodGet, "/-/work/"+enq.JobID, nil))
	var got struct {
		State      string `json:"state"`
		Answer     string `json:"answer"`
		AnswerHTML string `json:"answerHTML"`
	}
	if err := json.Unmarshal(getW.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode poll: %v", err)
	}
	if got.State != "done" {
		t.Errorf("state = %q, want done", got.State)
	}
	if got.Answer != "A **foo** is a thing." {
		t.Errorf("answer = %q, want the recorded answer", got.Answer)
	}
	if !strings.Contains(got.AnswerHTML, "<strong>foo</strong>") {
		t.Errorf("answerHTML = %q, want rendered markdown", got.AnswerHTML)
	}
}

func TestWorkDoneClosesJob(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusUnverified, []string{"part-01.md"})
	srv := serve.NewServer(dir)
	markWorkerConnected(t, srv)

	w := postVerify(t, srv, "test-tut")
	var resp struct {
		JobID string `json:"jobId"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	_ = claimNext(t, srv)

	doneW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(doneW, httptest.NewRequest(http.MethodPost, "/-/work/"+resp.JobID+"/done", nil))
	if doneW.Code != http.StatusNoContent {
		t.Fatalf("done = %d, want 204", doneW.Code)
	}

	getW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getW, httptest.NewRequest(http.MethodGet, "/-/work/"+resp.JobID, nil))
	var got struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(getW.Body.Bytes(), &got)
	if got.State != "done" {
		t.Errorf("state = %q, want done", got.State)
	}
}

func TestWorkAnswerUnknownJobIs404(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodPost, "/-/work/nope/answer",
		bytes.NewReader([]byte(`{"answer":"x"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("answer for unknown job = %d, want 404", w.Code)
	}
}

func TestWorkAnswerRejectsCrossOrigin(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodPost, "/-/work/1/answer",
		bytes.NewReader([]byte(`{"answer":"x"}`)))
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-origin answer = %d, want 403", w.Code)
	}
}

func TestWorkDoneRejectsCrossOrigin(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodPost, "/-/work/1/done", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-origin done = %d, want 403", w.Code)
	}
}

func TestVerifyRejectsCrossOrigin(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusUnverified, []string{"part-01.md"})
	srv := serve.NewServer(dir)

	req := httptest.NewRequest(http.MethodPost, "/-/verify/test-tut", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-origin verify = %d, want 403", w.Code)
	}
}

// A cross-origin page must not be able to claim (and thereby steal) a queued job
// from the real worker — GET /-/work dequeues, so it is state-changing despite
// the verb. The CLI worker sends no Origin header and stays admitted.
func TestWorkNextRejectsCrossOrigin(t *testing.T) {
	srv := serve.NewServer(t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/-/work", nil).WithContext(ctx)
	req.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin GET /-/work = %d, want 403", w.Code)
	}
}

func TestCorrectEnqueuesWhenWorkerConnected(t *testing.T) {
	dir := t.TempDir()
	// makeExtendTutorial lives in extend_test.go (same package) and writes each
	// part file to disk, which handleCorrect os.Stats.
	makeExtendTutorial(t, dir, "tut", store.StatusVerified, []string{"part-01.md"})
	srv := serve.NewServer(dir)
	markWorkerConnected(t, srv)

	body := []byte(`{"excerpt":"512 samples","note":"it is 1024"}`)
	req := httptest.NewRequest(http.MethodPost, "/-/correct/tut/part-01.md", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /-/correct = %d, want 200 (body=%q)", w.Code, w.Body.String())
	}
	var resp struct {
		Mode  string `json:"mode"`
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Mode != "queued" || resp.JobID == "" {
		t.Fatalf("response = %+v, want mode=queued with a job id", resp)
	}

	job := claimNext(t, srv)
	if job["type"] != "correct" {
		t.Errorf("job type = %v, want correct", job["type"])
	}
	if job["part"] != "part-01.md" {
		t.Errorf("job part = %v, want part-01.md", job["part"])
	}
	if job["excerpt"] != "512 samples" {
		t.Errorf("job excerpt = %v, want the selected text", job["excerpt"])
	}
	if job["note"] != "it is 1024" {
		t.Errorf("job note = %v, want the reader's note", job["note"])
	}

	// The worker closes a correction with `lathe work answer`, not `work done`,
	// because the browser shows its one-line report — including when the skill
	// deliberately changed nothing. handleWorkAnswer never inspects the job
	// type, so this needs no server change; the test pins the behaviour.
	ansBody := []byte(`{"answer":"left unchanged: couldn't find that passage"}`)
	ansReq := httptest.NewRequest(http.MethodPost, "/-/work/"+resp.JobID+"/answer", bytes.NewReader(ansBody))
	ansReq.Header.Set("Content-Type", "application/json")
	ansW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(ansW, ansReq)
	if ansW.Code != http.StatusNoContent {
		t.Fatalf("POST /-/work/{id}/answer = %d, want 204", ansW.Code)
	}

	getW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getW, httptest.NewRequest(http.MethodGet, "/-/work/"+resp.JobID, nil))
	var got map[string]any
	if err := json.Unmarshal(getW.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	if got["state"] != "done" {
		t.Errorf("state = %v, want done", got["state"])
	}
	if got["answer"] != "left unchanged: couldn't find that passage" {
		t.Errorf("answer = %v, want the worker's report", got["answer"])
	}
	// answerHTML stays gated to ask jobs — a correction report is one plain line.
	if _, ok := got["answerHTML"]; ok {
		t.Error("correct job carries answerHTML; want the plain answer only")
	}
}

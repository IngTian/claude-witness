package opencode

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestOpenCodeServerRunIsConcurrent proves the #22 mutex narrowing: N Run calls
// must be genuinely IN FLIGHT at once. The fake server blocks each request's reply
// poll until all N have arrived (a barrier). Under the OLD whole-request s.mu.Lock,
// request 2 could never start until request 1 returned, so the barrier would never
// release and the test would time out. With the lock narrowed to the closed-check,
// all N reach the barrier and it releases.
func TestOpenCodeServerRunIsConcurrent(t *testing.T) {
	const n = 4
	var arrived sync.WaitGroup
	arrived.Add(n)
	release := make(chan struct{})
	var maxInFlight int32
	var inFlight int32

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Each Run creates its own session id from the request; echo a per-session id
		// so the isolated-session assumption holds. We key off a session counter.
		switch r.Method {
		case http.MethodPost:
			if r.URL.Path == "/session" {
				// unique id per created session
				id := fmt.Sprintf("ses_%d", atomic.AddInt32(&sessionSeq, 1))
				_, _ = fmt.Fprintf(w, `{"id":%q}`, id)
				return
			}
			// prompt_async
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			// The reply poll: mark arrival, wait for the barrier, then answer. This is
			// where concurrency is observable — all N must be parked here at once.
			cur := atomic.AddInt32(&inFlight, 1)
			for {
				old := atomic.LoadInt32(&maxInFlight)
				if cur <= old || atomic.CompareAndSwapInt32(&maxInFlight, old, cur) {
					break
				}
			}
			arrived.Done()
			<-release
			atomic.AddInt32(&inFlight, -1)
			// reply keyed to whatever message id was requested
			_, _ = w.Write([]byte(`[
				{"info":{"id":"msg_request","role":"user"},"parts":[{"id":"u","type":"text","text":"DATA"}]},
				{"info":{"id":"msg_reply","role":"assistant"},"parts":[{"id":"a","type":"text","text":"RESULT"}]}
			]`))
		case http.MethodDelete:
			_, _ = w.Write([]byte(`{}`))
		}
	})
	ts := httptest.NewServer(h)
	defer ts.Close()
	srv := &OpenCodeServer{baseURL: ts.URL, authHeader: "Basic test", client: ts.Client()}

	// Release the barrier once all N requests have arrived (or fail fast on timeout).
	go func() {
		done := make(chan struct{})
		go func() { arrived.Wait(); close(done) }()
		select {
		case <-done:
			close(release)
		case <-time.After(10 * time.Second):
			// Leave release open; the Runs will hit their own ctx deadline and the test
			// assertion below will report the serialization.
		}
	}()

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			_, errs[i] = srv.Run(ctx, "", "EXTRACT", "DATA")
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Run %d failed (serialized? barrier never released): %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&maxInFlight); got != n {
		t.Fatalf("max concurrent Run calls = %d, want %d (mutex still serializes the whole request)", got, n)
	}
}

// sessionSeq gives each created fake session a unique id across the concurrency
// test's goroutines.
var sessionSeq int32

// TestOpenCodeServerRunRejectsAfterClose keeps the closed-check honest after the
// narrowing: a Run started once the server is marked closed still errors fast.
func TestOpenCodeServerRunRejectsAfterClose(t *testing.T) {
	srv := &OpenCodeServer{baseURL: "http://127.0.0.1:0", authHeader: "Basic test", client: http.DefaultClient}
	srv.mu.Lock()
	srv.closed = true
	srv.mu.Unlock()
	if _, err := srv.Run(context.Background(), "", "EXTRACT", "DATA"); err == nil {
		t.Fatal("Run on a closed server should error")
	}
}

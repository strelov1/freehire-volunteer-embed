package teiproxy_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/strelov1/freehire-volunteer-embed/internal/teiproxy"
)

func TestHandler_ForwardsBodyAndResponseVerbatim(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Write([]byte("echo:" + string(body)))
	}))
	defer upstream.Close()

	d := teiproxy.NewDispatcher(256)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.RunWorkers(ctx, 2)

	h := teiproxy.NewHandler(d, upstream.URL, http.DefaultClient)

	req := httptest.NewRequest(http.MethodPost, "/high/embed", strings.NewReader("payload"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "echo:payload" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "echo:payload")
	}
}

func TestHandler_HighLanePreferredOverLowLaneUnderContention(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // every upstream call blocks until the test releases it
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	d := teiproxy.NewDispatcher(256)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.RunWorkers(ctx, 1) // single worker: makes the race deterministic

	h := teiproxy.NewHandler(d, upstream.URL, http.DefaultClient)

	var mu sync.Mutex
	var order []string
	fire := func(path, label string) {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("x"))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		mu.Lock()
		order = append(order, label)
		mu.Unlock()
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); fire("/low/embed", "low-first") }()
	time.Sleep(20 * time.Millisecond) // let it occupy the single worker

	wg.Add(2)
	go func() { defer wg.Done(); fire("/low/embed", "low-second") }()
	go func() { defer wg.Done(); fire("/high/embed", "high") }()
	time.Sleep(20 * time.Millisecond) // let both queue up behind the busy worker

	close(release)
	wg.Wait()

	if len(order) != 3 || order[0] != "low-first" {
		t.Fatalf("order = %v, want low-first first (it already held the worker)", order)
	}
	if order[1] != "high" {
		t.Errorf("order = %v, want \"high\" processed before \"low-second\" despite arriving after it", order)
	}
}

func TestHandler_UnknownPath_Returns404(t *testing.T) {
	d := teiproxy.NewDispatcher(256)
	h := teiproxy.NewHandler(d, "http://127.0.0.1:1", http.DefaultClient)

	req := httptest.NewRequest(http.MethodPost, "/nope", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

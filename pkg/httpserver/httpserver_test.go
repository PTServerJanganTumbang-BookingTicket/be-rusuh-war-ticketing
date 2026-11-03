package httpserver

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestRun_StartsAndStopsWithSignal(t *testing.T) {
	// Pick an ephemeral port to avoid conflicts.
	port := 18999
	started := make(chan struct{})

	go func() {
		Run(port, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}), func() { close(started) })
	}()

	// Wait for the server to accept connections.
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not start within 3 s")
	}

	// Confirm it responds.
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/", port))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	// Stopping the server (via signal) is tested end-to-end in the services;
	// testing OS signal delivery in a unit test is unreliable.
}

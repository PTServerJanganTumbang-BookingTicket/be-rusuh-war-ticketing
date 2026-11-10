// Package httpserver runs an HTTP server until a SIGINT or SIGTERM arrives,
// then shuts it down gracefully.
package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Run creates an HTTP server on the given port, starts it in a goroutine, and
// blocks until a SIGINT or SIGTERM is received. It then attempts a graceful
// shutdown within five seconds.
//
// onStart is called once the port is bound, so the server already accepts
// connections when it runs; use it to emit a startup log line with any
// service-specific fields (e.g. mode or feature flags).
func Run(port int, handler http.Handler, onStart func()) {
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		ReadHeaderTimeout: 1 * time.Second,
		Handler:           handler,
	}

	// Bind before onStart: calling it before ListenAndServe let callers (and
	// tests) observe "started" while the port was not yet listening.
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		slog.Error("Failed to start", slog.Any("err", err))
		os.Exit(1)
	}
	onStart()

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("Failed to serve", slog.Any("err", err))
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("Shutdown Server ...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server Shutdown:", slog.Any("err", err))
	}
}

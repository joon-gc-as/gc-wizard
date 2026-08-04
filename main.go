package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"github.com/joho/godotenv"
	"github.com/gummicube/gc-wizard/internal/proxy"
	"github.com/gummicube/gc-wizard/internal/server"
)

func gracefulShutdown(httpServer *http.Server, done chan bool) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()

	log.Println("shutting down gracefully, press Ctrl+C again to force")
	stop() // ctrl+c to force shutdown

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown with error: %v", err)
	}
	log.Println("Server exiting")
	done <- true
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatalf("Error loading .env file: %v", err)
	}
	wiz := server.Wizard()

	// local reverse proxy from smee.io to this current app
	proxyCtx, stopProxy := context.WithCancel(context.Background())
	defer stopProxy()
	if smeeURL := os.Getenv("SMEE_URL"); smeeURL != "" {
		go proxy.ForwardGHEventsToLocalApp(proxyCtx, smeeURL)
	}

	done := make(chan bool, 1)
	go gracefulShutdown(wiz, done)

	fmt.Printf("The Gummicube Wizard started on port:%s\n", wiz.Addr)
	err = wiz.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		panic(fmt.Sprintf("http server error: %s", err))
	}

	// wait for graceful shutdown
	<-done
	log.Println("Graceful shutdown complete.")
}

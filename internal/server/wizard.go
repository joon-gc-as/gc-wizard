package server

import (
	"fmt"
	"net/http"
	"os"
	"time"
	"github.com/gummicube/gc-wizard/internal/router"
)


// New builds the HTTP server for the wizard API, wired to the wizard router.
func Wizard() *http.Server {
	fmt.Printf("the port number %s\n", os.Getenv("APP_PORT"))
	return &http.Server{
		Addr:         fmt.Sprintf("0.0.0.0:%s", os.Getenv("APP_PORT")),
		Handler:      router.WizardHandler(),
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
}

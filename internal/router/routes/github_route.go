package routes

import (
	"encoding/json"
	"net/http"
	"github.com/go-chi/chi/v5"
	"github.com/gummicube/gc-wizard/internal/remotes"
)

func Github() chi.Router {
	r := chi.NewRouter()
	r.Get("/repos", func(w http.ResponseWriter, r *http.Request) {
		ghs := remotes.Service
		repos, err := ghs.ListRepositories(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(repos)
		w.WriteHeader(http.StatusOK)
	})
	return r
}


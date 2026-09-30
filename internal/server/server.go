package server

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func New() http.Handler {
	router := chi.NewRouter()
	router.Get("/livez", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(response, "{\"status\":\"alive\"}\n")
	})

	return router
}

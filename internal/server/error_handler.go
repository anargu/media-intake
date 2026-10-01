package server

import (
	"encoding/json"
	"net/http"

	"github.com/anargu/media-intake/internal/apierror"
)

func writeError(w http.ResponseWriter, publicErr apierror.PublicError) {
	w.Header().Set("Content-Type", "application/json")

	w.WriteHeader(publicErr.HTTPCode)
	_ = json.NewEncoder(w).Encode(publicErr)
}

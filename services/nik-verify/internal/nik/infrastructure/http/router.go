package http

import "net/http"

// RegisterRoutes mounts the NIK routes on the given mux.
// Route patterns use Go 1.22+ method-prefixed syntax.
func RegisterRoutes(mux *http.ServeMux, h Nik) {
	mux.HandleFunc("POST /nik/validate", h.Validate)
}

package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func NewRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", h.Healthz)
	r.Get("/readyz", h.Readyz)

	r.Route("/v1", func(r chi.Router) {
		r.Post("/documents", h.UploadDocument)
		r.Get("/reviews/pending", h.PendingReviews)
		r.Route("/documents/{documentId}", func(r chi.Router) {
			r.Get("/status", func(w http.ResponseWriter, r *http.Request) {
				h.GetStatus(w, r, chi.URLParam(r, "documentId"))
			})
			r.Get("/result", func(w http.ResponseWriter, r *http.Request) {
				h.GetResult(w, r, chi.URLParam(r, "documentId"))
			})
			r.Post("/review", func(w http.ResponseWriter, r *http.Request) {
				h.SubmitReview(w, r, chi.URLParam(r, "documentId"))
			})
		})
	})

	return r
}

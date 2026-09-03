// Package router builds the chi router wiring every HTTP route to its handler
// and middleware. It's the single source of truth for the route table, used
// by both the production entrypoint (cmd/server) and the integration tests.
package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/cutmax/cutmax-backend/internal/handlers"
	"github.com/cutmax/cutmax-backend/internal/middleware"
)

func New() http.Handler {
	r := chi.NewRouter()

	// Global middleware
	r.Use(middleware.SecurityHeaders)
	r.Use(middleware.CORS)
	r.Use(middleware.CSRF)

	// Public routes
	r.Get("/api/health", handlers.HandleHealth)
	r.Get("/api/public/csrf", handlers.HandleCSRF)
	r.Get("/api/public/products", handlers.HandleListProducts)
	r.Get("/api/public/products/{sku}", handlers.HandleGetProduct)
	r.Get("/api/public/price-tiers", handlers.HandleListTiers)
	r.Route("/api/public/auth", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(middleware.RateLimit(middleware.RLCustomerReg))
			r.Post("/register", handlers.HandleRegister)
		})
		r.Group(func(r chi.Router) {
			r.Use(middleware.RateLimit(middleware.RLCustomerLogin))
			r.Post("/login", handlers.HandleLogin)
		})
		r.Post("/logout", handlers.HandleLogout)
		r.Get("/me", func(w http.ResponseWriter, r *http.Request) {
			middleware.OptionalCustomer(http.HandlerFunc(handlers.HandleMe)).ServeHTTP(w, r)
		})
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireCustomer)
			r.Get("/my-enquiries", handlers.HandleMyEnquiries)
		})
	})
	r.Group(func(r chi.Router) {
		r.Use(middleware.RateLimit(middleware.RLEnquirySubmit))
		r.Use(middleware.OptionalCustomer)
		r.Post("/api/public/enquiries", handlers.HandleCreateEnquiry)
	})
	r.Group(func(r chi.Router) {
		r.Use(middleware.RateLimit(middleware.RLSubscribe))
		r.Post("/api/public/subscribe", handlers.HandleSubscribe)
	})

	// Admin routes
	r.Route("/api/admin/auth", func(r chi.Router) {
		r.Use(middleware.NoStore)
		r.Group(func(r chi.Router) {
			r.Use(middleware.RateLimit(middleware.RLAdminLogin))
			r.Post("/login", handlers.HandleAdminLogin)
		})
		r.Post("/logout", handlers.HandleAdminLogout)
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAdmin)
			r.Get("/me", handlers.HandleAdminMe)
			r.Group(func(r chi.Router) {
				r.Use(middleware.RateLimit(middleware.RLAdminPassword))
				r.Put("/password", handlers.HandleAdminChangePassword)
			})
		})
	})
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAdmin)
		r.Use(middleware.NoStore)
		r.Get("/api/admin/products", handlers.HandleAdminProducts)
		r.Post("/api/admin/products", handlers.HandleAdminProducts)
		r.Post("/api/admin/products/bulk-delete", handlers.HandleAdminProductsBulkDelete)
		r.Post("/api/admin/products/bulk-activate", handlers.HandleAdminProductsBulkActivate)
		r.Put("/api/admin/products/{id}", handlers.HandleAdminProduct)
		r.Delete("/api/admin/products/{id}", handlers.HandleAdminProduct)
		r.Put("/api/admin/products/{id}/price-breaks", handlers.HandleAdminProductPriceBreaks)
		r.Get("/api/admin/price-tiers", handlers.HandleAdminTiers)
		r.Post("/api/admin/price-tiers", handlers.HandleAdminTiers)
		r.Put("/api/admin/price-tiers/{id}", handlers.HandleAdminTier)
		r.Delete("/api/admin/price-tiers/{id}", handlers.HandleAdminTier)
		r.Get("/api/admin/enquiries", handlers.HandleAdminEnquiries)
		r.Get("/api/admin/enquiries/{id}", handlers.HandleAdminEnquiry)
		r.Get("/api/admin/enquiries/{id}/pdf", handlers.HandleAdminEnquiryPDF)
		r.Put("/api/admin/enquiries/{id}", handlers.HandleAdminEnquiry)
		r.Delete("/api/admin/enquiries/{id}", handlers.HandleAdminEnquiry)
		r.Get("/api/admin/settings", handlers.HandleAdminSettings)
		r.Put("/api/admin/settings", handlers.HandleAdminSettings)
		r.Get("/api/admin/stats", handlers.HandleAdminStats)
		r.Get("/api/admin/audit", handlers.HandleAdminAudit)
		r.Post("/api/admin/bulk/prices", handlers.HandleBulkPrices)
		r.Group(func(r chi.Router) {
			r.Use(middleware.RateLimit(middleware.RLBulkProducts))
			r.Post("/api/admin/bulk/products", handlers.HandleBulkProducts)
			r.Post("/api/admin/bulk/stock", handlers.HandleBulkStock)
		})
		r.Group(func(r chi.Router) {
			r.Use(middleware.RateLimit(middleware.RLBulkImages))
			r.Post("/api/admin/bulk/images", handlers.HandleBulkImages)
		})
		r.Group(func(r chi.Router) {
			r.Use(middleware.RateLimit(middleware.RLImageUpload))
			r.Post("/api/admin/uploads", handlers.HandleAdminUpload)
		})
		r.Get("/api/admin/media", handlers.HandleAdminMediaList)
		r.Delete("/api/admin/media/{id}", handlers.HandleAdminMediaDelete)
		r.Group(func(r chi.Router) {
			r.Use(middleware.RateLimit(middleware.RLImageUpload))
			r.Post("/api/admin/media", handlers.HandleAdminMediaUpload)
		})
	})

	// Serve uploads
	r.Get("/api/uploads/*", handlers.HandleServeUpload)

	return r
}

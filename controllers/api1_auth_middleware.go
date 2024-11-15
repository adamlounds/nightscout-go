package controllers

import (
	"github.com/adamlounds/nightscout-go/middleware"
	"github.com/adamlounds/nightscout-go/models"
	slogctx "github.com/veqryn/slog-context"
	"log/slog"
	"net/http"
)

type ApiV1AuthnMiddleware struct {
	*models.AuthService
}

func (a ApiV1AuthnMiddleware) SetAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := slogctx.FromCtx(ctx)

		apiSecretHash := r.Header.Get("api-secret")
		if apiSecretHash == "" {
			apiSecretHash = r.URL.Query().Get("secret")
		}
		authToken := r.URL.Query().Get("token")

		authn := a.AuthFromHTTP(ctx, apiSecretHash, authToken)

		log.Debug("SetAuthentication", slog.Any("authn", authn))
		ctx = middleware.WithAuthn(ctx, authn)
		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}

func (a ApiV1AuthnMiddleware) Authz(requiredRole string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			log := slogctx.FromCtx(ctx)
			authn := middleware.GetAuthn(ctx)
			log.Debug("Authzmw got authn from ctx", slog.Any("authn", authn), slog.String("requiredRole", requiredRole))

			if a.IsPermitted(ctx, authn, requiredRole) {
				log.Debug("Authzmw ok", slog.String("requiredRole", requiredRole))
				next.ServeHTTP(w, r)
			} else {
				log.Debug("Authzmw rejected", slog.String("requiredRole", requiredRole))
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		})
	}
}

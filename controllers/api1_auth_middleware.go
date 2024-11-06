package controllers

import (
	"fmt"
	"github.com/adamlounds/nightscout-go/middleware"
	"github.com/adamlounds/nightscout-go/models"
	"net/http"
)

type ApiV1AuthnMiddleware struct {
	*models.AuthService
}

func (a ApiV1AuthnMiddleware) SetAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		apiSecretHash := r.Header.Get("api-secret")
		if apiSecretHash == "" {
			apiSecretHash = r.URL.Query().Get("secret")
		}
		authToken := r.URL.Query().Get("token")

		authn := a.AuthFromHTTP(ctx, apiSecretHash, authToken)

		fmt.Printf("SetAuthentication %v\n", authn)
		ctx = middleware.WithAuthn(ctx, authn)
		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}

func (a ApiV1AuthnMiddleware) Authz(requiredRole string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			authn := middleware.GetAuthn(ctx)
			fmt.Printf("authz (%s) got ctx authn [%#v]\n", requiredRole, authn)

			if a.IsPermitted(ctx, authn, requiredRole) {
				fmt.Printf("authz (%s) is permitted\n", requiredRole)
				next.ServeHTTP(w, r)
			} else {
				fmt.Printf("authz (%s) is NOT PERMITTED\n", requiredRole)
			}

			next.ServeHTTP(w, r)
		})
	}

}

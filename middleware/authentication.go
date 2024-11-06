package middleware

import (
	"context"
	"github.com/adamlounds/nightscout-go/models"
)

// Key to use when setting the request Authn
type ctxKeyAuthn int

// AuthnKey is the key that holds the authn details in a request middleware.
const AuthnKey ctxKeyAuthn = 0

func WithAuthn(ctx context.Context, authn *models.Authn) context.Context {
	return context.WithValue(ctx, AuthnKey, authn)
}

func GetAuthn(ctx context.Context) *models.Authn {
	val := ctx.Value(AuthnKey)
	authn, ok := val.(*models.Authn)
	if !ok {
		return nil
	}
	return authn
}

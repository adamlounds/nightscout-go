package repository

import (
	"context"
	"fmt"
	"github.com/adamlounds/nightscout-go/models"
	pgstore "github.com/adamlounds/nightscout-go/stores/postgres"
	"strings"
)

type PostgresAuthRepository struct {
	*pgstore.PostgresStore
	APISecretHash string
	DefaultRole   string
}

func NewPostgresAuthRepository(pgstore *pgstore.PostgresStore, APISecretHash string, DefaultRole string) *PostgresAuthRepository {
	return &PostgresAuthRepository{pgstore, APISecretHash, DefaultRole}
}

func (p PostgresAuthRepository) GetAPISecretHash(ctx context.Context) string {
	return "945a6dadff2d6cd1e8faf31b2da50ce467c440e1"
}

func (p PostgresAuthRepository) GetDefaultRole(ctx context.Context) string {
	return p.DefaultRole
}

var unknownAuthSubject = &models.AuthSubject{Name: "anonymous", RoleNames: []string{}}

func (p PostgresAuthRepository) FetchAuthSubjectByAuthToken(ctx context.Context, authToken string) *models.AuthSubject {
	if authToken == "" {
		return unknownAuthSubject
	}
	name, _, found := strings.Cut(authToken, "-")
	if !found {
		fmt.Println("auth token is invalid, should be name-hash")
		return unknownAuthSubject
	}

	// TODO move to db
	hashes := map[string]*models.AuthSubject{
		"ffs-358de43470f328f3": {
			Name:      name,
			RoleNames: []string{"admin", "cgm-uploader"},
		},
	}

	authSubject, ok := hashes[authToken]
	if !ok {
		fmt.Println("auth token not found")
		return unknownAuthSubject
	}
	return authSubject
}

// Decision for future: roles and AuthSubjects should be cached (they are in ns-js)
// Keep copy of fetched users/roles with configurable ttl (infinite by default,
// expiryTime = null). Updates to users or roles should purge both caches, so a
// single-node system will not see stale data.

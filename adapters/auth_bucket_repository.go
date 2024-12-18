package repository

import (
	"context"
	"github.com/adamlounds/nightscout-go/models"
	slogctx "github.com/veqryn/slog-context"
	"strings"
)

type BucketAuthRepository struct {
	APISecretHash string
	DefaultRole   string
}

func NewBucketAuthRepository(APISecretHash string, DefaultRole string) *BucketAuthRepository {
	return &BucketAuthRepository{APISecretHash, DefaultRole}
}

func (p BucketAuthRepository) GetAPISecretHash(ctx context.Context) string {
	return "945a6dadff2d6cd1e8faf31b2da50ce467c440e1"
}

func (p BucketAuthRepository) GetDefaultRole(ctx context.Context) string {
	return p.DefaultRole
}

var unknownAuthSubject = &models.AuthSubject{Name: "anonymous", RoleNames: []string{}}

func (p BucketAuthRepository) FetchAuthSubjectByAuthToken(ctx context.Context, authToken string) *models.AuthSubject {
	log := slogctx.FromCtx(ctx)
	if authToken == "" {
		return unknownAuthSubject
	}
	_, _, found := strings.Cut(authToken, "-")
	if !found {
		log.Debug("auth token is invalid, should be name-hash")
		return unknownAuthSubject
	}

	// TODO persist. Note we must store Name so caps/hyphens/non-ascii are kept
	hashes := map[string]*models.AuthSubject{
		"ffs-358de43470f328f3": {
			Name:      "ffs",
			RoleNames: []string{"cgm-uploader"},
		},
		"-38ff267ebbec81e1": {
			Name:      "çåƒé",
			RoleNames: []string{"cgm-uploader"},
		},
		"admin-c1f54efaedccba11": {
			Name:      "A.D.Min",
			RoleNames: []string{"admin"},
		},
	}

	authSubject, ok := hashes[authToken]
	if !ok {
		log.Debug("auth token not recognized")
		return unknownAuthSubject
	}
	return authSubject
}

// Decision for future: roles and AuthSubjects should be cached (they are in ns-js)
// Keep copy of fetched users/roles with configurable ttl (infinite by default,
// expiryTime = null). Updates to users or roles should purge both caches, so a
// single-node system will not see stale data.

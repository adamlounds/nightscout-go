package repository

import (
	"context"
	"github.com/adamlounds/nightscout-go/models"
	slogctx "github.com/veqryn/slog-context"
	"log/slog"
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

var unknownSubject = &models.AuthSubject{Name: "anonymous", RoleNames: []string{}}

var subjectsByToken = map[string]*models.AuthSubject{
	"ffs-358de43470f328f3": {
		Name:      "ffs",
		RoleNames: []string{"cgm-uploader", "readable"},
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

var tokensByHash = map[string]string{
	"b9e80b4cae356572fc11e40fd68b6de6c7fa995c": "ffs-358de43470f328f3",
	"a62e1ed038ae13505860ebf0756abf733e5825e1": "-38ff267ebbec81e1",
	"f6c0ba8c0f3a96a6a593800b71cc0590fbedf2e4": "admin-c1f54efaedccba11",
}

func (p BucketAuthRepository) FetchSubjectByToken(ctx context.Context, token string) *models.AuthSubject {
	log := slogctx.FromCtx(ctx)
	if token == "" {
		return unknownSubject
	}
	_, _, found := strings.Cut(token, "-")
	if !found {
		log.Debug("auth token is invalid, should be name-hash")
		return unknownSubject
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

	authSubject, ok := hashes[token]
	if !ok {
		log.Debug("auth token not recognized")
		return unknownSubject
	}
	return authSubject
}

func (p BucketAuthRepository) FetchSubjectByHash(ctx context.Context, hash string) *models.AuthSubject {
	log := slogctx.FromCtx(ctx)
	if hash == "" {
		return unknownSubject
	}
	if len(hash) != 40 {
		log.Debug("hash is invalid, should be 40 chars", slog.String("hash", hash))
		return unknownSubject
	}

	token, ok := tokensByHash[hash]
	if !ok {
		log.Debug("hash not recognized", slog.String("hash", hash))
		return unknownSubject
	}

	authSubject, ok := subjectsByToken[token]
	if !ok {
		log.Warn("hash-derived token not found???")
		return unknownSubject
	}
	return authSubject
}

func (p BucketAuthRepository) FetchAllRoles(ctx context.Context) []*models.Role {
	return []*models.Role{}
}

// Decision for future: roles and AuthSubjects should be cached (they are in ns-js)
// Keep copy of fetched users/roles with configurable ttl (infinite by default,
// expiryTime = null). Updates to users or roles should purge both caches, so a
// single-node system will not see stale data.

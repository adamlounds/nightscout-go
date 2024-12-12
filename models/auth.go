package models

import (
	"context"
	slogctx "github.com/veqryn/slog-context"
	"log/slog"
	"time"
)

type AuthService struct {
	AuthRepository
}

type AuthSubject struct {
	CreatedTime time.Time
	UpdatedTime time.Time
	Oid         string
	Name        string
	Notes       string
	RoleNames   []string
	ID          int
}

type Role struct {
	CreatedTime time.Time
	UpdatedTime time.Time
	Name        string
	Notes       string
	Permissions []string
	ID          int
}

type AuthRepository interface {
	GetAPISecretHash(ctx context.Context) string
	GetDefaultRole(ctx context.Context) string
	//FetchAllRoles(ctx middleware.Context) []*Role
	FetchAuthSubjectByAuthToken(ctx context.Context, authToken string) *AuthSubject
}

type Authn struct {
	AuthSubject   *AuthSubject
	ApiSecretHash string
	AuthToken     string
}

func (a Authn) LogValue() slog.Value {
	hasSecret := a.ApiSecretHash != ""
	hasToken := a.AuthToken != ""
	return slog.GroupValue(
		slog.Bool("hasSecret", hasSecret),
		slog.Bool("hasToken", hasToken),
		slog.Any("authSubject", a.AuthSubject.Name),
	)
}

func (service *AuthService) AuthFromHTTP(ctx context.Context, apiSecretHash string, authToken string) *Authn {
	authSubject := service.FetchAuthSubject(ctx, apiSecretHash, authToken)

	return &Authn{
		ApiSecretHash: apiSecretHash,
		AuthToken:     authToken,
		AuthSubject:   authSubject,
	}
}

var adminAuthSubject = &AuthSubject{Name: "admin", RoleNames: []string{"admin"}}

func (service *AuthService) FetchAuthSubject(ctx context.Context, apiSecretHash string, authToken string) *AuthSubject {
	log := slogctx.FromCtx(ctx)
	if service.IsAPISecretHashValid(ctx, apiSecretHash) {
		log.Debug("api secret is valid, it's the admin user")
		return adminAuthSubject
	}

	as := service.FetchAuthSubjectByAuthToken(ctx, authToken)
	if as.IsAnonymous() {
		// api-secret header can contain a token 🤪
		as = service.FetchAuthSubjectByAuthToken(ctx, apiSecretHash)
		if !as.IsAnonymous() {
			log.Debug("api secret was an auth token", slog.String("name", as.Name))
		}
	}
	return as
}

var defaultRoles = map[string]*Role{
	"activity":            {Name: "activity", Permissions: []string{"api:activity:create"}},
	"admin":               {Name: "admin", Permissions: []string{"*"}},
	"careportal":          {Name: "careportal", Permissions: []string{"api:treatments:create"}},
	"denied":              {Name: "denied", Permissions: []string{}},
	"devicestatus-upload": {Name: "devicestatus-upload", Permissions: []string{"api:devicestatus:create"}},
	"readable":            {Name: "readable", Permissions: []string{"*:*:read"}},
	"status-only":         {Name: "status-only", Permissions: []string{"api:status:read"}},
}

var additionalRoles = map[string]*Role{
	"cgm-uploader": {Name: "cgm-uploader", Permissions: []string{"api:entries:read", "api:entries:create"}},
}

func (service *AuthService) IsPermitted(ctx context.Context, a *Authn, requiredPermission string) bool {
	log := slogctx.FromCtx(ctx)
	for _, roleName := range a.AuthSubject.RoleNames {
		role, ok := defaultRoles[roleName]
		if !ok {
			role, ok = additionalRoles[roleName]
			if !ok {
				log.Debug("role not found", "roleName", roleName)
				continue
			}
		}

		for _, permission := range role.Permissions {
			if permission == requiredPermission {
				log.Debug("named role is allowed",
					slog.String("roleName", roleName),
					slog.String("perm", permission),
					slog.String("requiredPerm", requiredPermission),
				)
				return true
			}

			// nightscout uses shiro-style permissions https://shiro.apache.org/permissions.html
			// eg api:entries:read, api:*:read, admin:api:subjects:read
			// TODO: implement full shiro permission checks

			if permission == "*" {
				log.Debug("admin role is allowed",
					slog.String("roleName", roleName),
					slog.String("perm", permission),
					slog.String("requiredPerm", requiredPermission),
				)
				return true
			}
		}
	}

	return false
}

func (service *AuthService) IsAPISecretHashValid(ctx context.Context, apiSecretHash string) (isValid bool) {
	return apiSecretHash == service.AuthRepository.GetAPISecretHash(ctx)
}

func (as *AuthSubject) IsAnonymous() bool {
	return as.Name == "anonymous"
}

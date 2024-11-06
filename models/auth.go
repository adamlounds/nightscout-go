package models

import (
	"context"
	"fmt"
	"time"
)

type AuthService struct {
	AuthRepository
}

type AuthSubject struct {
	ID          int
	Oid         string
	Name        string
	Notes       string
	RoleNames   []string
	CreatedTime time.Time
	UpdatedTime time.Time
}

type Role struct {
	ID          int
	Name        string
	Permissions []string
	Notes       string
	CreatedTime time.Time
	UpdatedTime time.Time
}

type AuthRepository interface {
	GetAPISecretHash(ctx context.Context) string
	GetDefaultRole(ctx context.Context) string
	//FetchAllRoles(ctx middleware.Context) []*Role
	FetchAuthSubjectByAuthToken(ctx context.Context, authToken string) *AuthSubject
}

type Authn struct {
	ApiSecretHash string
	AuthToken     string
	AuthSubject   *AuthSubject
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
	if service.IsAPISecretHashValid(ctx, apiSecretHash) {
		fmt.Println("api secret is valid, it's the admin user")
		return adminAuthSubject
	}

	return service.FetchAuthSubjectByAuthToken(ctx, authToken)
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
	for _, roleName := range a.AuthSubject.RoleNames {
		role, ok := defaultRoles[roleName]
		if !ok {
			role, ok = additionalRoles[roleName]
			if !ok {
				fmt.Printf("role %s not found\n", roleName)
				continue
			}
		}

		for _, permission := range role.Permissions {
			if permission == requiredPermission {
				fmt.Printf("role %s is allowed got [%s] need [%s]\n", roleName, permission, requiredPermission)
				return true
			}

			// nightscout uses shiro-style permissions https://shiro.apache.org/permissions.html
			// eg api:entries:read, api:*:read, admin:api:subjects:read
			// TODO: implement full shiro permission checks

			if permission == "*" {
				fmt.Printf("role %s is allowed. got [%s] need [%s]\n", roleName, permission, requiredPermission)
				return true
			}
		}
	}

	return false
}

func (service *AuthService) IsAPISecretHashValid(ctx context.Context, apiSecretHash string) (isValid bool) {
	return apiSecretHash == service.AuthRepository.GetAPISecretHash(ctx)
}

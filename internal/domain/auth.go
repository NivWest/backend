package domain

import (
	"context"
	"time"

	"backend/internal/models"
)

// User roles stored in models.User.Role.
const (
	RoleMember = "member"
	RoleAdmin  = "admin"
	RoleGuest  = "guest"
)

// Guest accounts (story NW-01). A guest is a normal models.User with:
//
//	Role     = RoleGuest
//	Name     = GuestName
//	Balance  = GuestStartingBalance
//	GoogleID = GuestGoogleIDPrefix + <uuid>             // column is unique and not null
//	Email    = "guest-" + <uuid> + "@" + GuestEmailDomain
const (
	GuestName            = "Guest trader"
	GuestStartingBalance = 100000.0
	GuestGoogleIDPrefix  = "guest:"
	GuestEmailDomain     = "guest.tradeedu.invalid"

	// GuestRetention is how long after its newest session expired a guest is kept before it is purged.
	GuestRetention = 24 * time.Hour
	// GuestPurgeInterval is how often the purge ticker started in internal/app runs.
	GuestPurgeInterval = time.Hour
)

// API error codes, returned as ErrorResponse.Code.
const (
	CodeValidation      = "VALIDATION_ERROR"
	CodeUnauthenticated = "UNAUTHENTICATED"
	CodeForbidden       = "FORBIDDEN"
	CodeUserNotFound    = "USER_NOT_FOUND"
	CodeRateLimited     = "RATE_LIMITED"
	CodeInternal        = "INTERNAL_ERROR"
)

// ErrorResponse is the error body of new and changed endpoints (api/openapi.yaml#/components/schemas/Error).
type ErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// ProfileResponse is the public view of a user (api/openapi.yaml#/components/schemas/Profile).
// Handlers return this instead of models.User so google_id and associations never leak.
type ProfileResponse struct {
	ID      uint    `json:"id"`
	Name    string  `json:"name"`
	Email   string  `json:"email"`
	Role    string  `json:"role"`
	Balance float64 `json:"balance"`
	IsGuest bool    `json:"is_guest"`
}

// NewProfileResponse maps a user to its public profile.
func NewProfileResponse(u *models.User) ProfileResponse {
	return ProfileResponse{
		ID:      u.ID,
		Name:    u.Name,
		Email:   u.Email,
		Role:    u.Role,
		Balance: u.Balance,
		IsGuest: u.Role == RoleGuest,
	}
}

// Features lists the optional switches enabled on this deployment (api/openapi.yaml#/components/schemas/Features).
type Features struct {
	GuestAuth bool `json:"guest_auth"`
}

// HealthResponse is the body of GET /health (api/openapi.yaml#/components/schemas/Health).
type HealthResponse struct {
	Status   string   `json:"status"`
	Version  string   `json:"version"`
	Features Features `json:"features"`
}

// GuestRepository removes expired guest accounts.
type GuestRepository interface {
	// DeleteExpiredGuests hard-deletes (Unscoped) every user with Role == RoleGuest that was created
	// before cutoff and has no session with expires_at >= cutoff, together with its dependent rows:
	// sessions, positions, transactions, watchlist items, watchlists and user_favorites.
	// It runs in one transaction and returns the number of users deleted.
	DeleteExpiredGuests(ctx context.Context, cutoff time.Time) (int64, error)
}

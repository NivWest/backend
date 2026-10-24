package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/testutil"
)

// sessionRecorder wraps testutil.AuthRepo to keep the full sessions (including ExpiresAt),
// which the shared fake does not store, and to inject CreateSession errors.
type sessionRecorder struct {
	*testutil.AuthRepo
	created []models.Session
	err     error
}

func (r *sessionRecorder) CreateSession(ctx context.Context, session *models.Session) error {
	if r.err != nil {
		return r.err
	}
	r.created = append(r.created, *session)
	return r.AuthRepo.CreateSession(ctx, session)
}

var guestTestNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func newGuestAuthService(users *testutil.UserRepo, auth domain.AuthRepository) *AuthService {
	cfg := &config.Config{}
	cfg.Auth.GuestSessionTTL = 90 * time.Minute
	cfg.Auth.SessionTTL = 24 * time.Hour
	svc := NewAuthService(users, auth, cfg)
	svc.now = func() time.Time { return guestTestNow }
	return svc
}

func TestAuthService_GuestLogin_CreatesGuestUserAndSession(t *testing.T) {
	users := testutil.NewUserRepo()
	auth := &sessionRecorder{AuthRepo: testutil.NewAuthRepo()}
	svc := newGuestAuthService(users, auth)

	user, sessionID, err := svc.GuestLogin(context.Background())
	if err != nil {
		t.Fatalf("GuestLogin() error = %v", err)
	}
	if user.ID == 0 {
		t.Error("user.ID = 0, want the ID assigned by the repository")
	}
	if user.Role != domain.RoleGuest {
		t.Errorf("Role = %q, want %q", user.Role, domain.RoleGuest)
	}
	if user.Name != "Guest trader" {
		t.Errorf("Name = %q, want %q", user.Name, "Guest trader")
	}
	if user.Balance != 100000 {
		t.Errorf("Balance = %v, want 100000", user.Balance)
	}
	if !strings.HasPrefix(user.GoogleID, domain.GuestGoogleIDPrefix) || len(user.GoogleID) <= len(domain.GuestGoogleIDPrefix) {
		t.Errorf("GoogleID = %q, want prefix %q followed by a uuid", user.GoogleID, domain.GuestGoogleIDPrefix)
	}
	suffix := strings.TrimPrefix(user.GoogleID, domain.GuestGoogleIDPrefix)
	if want := "guest-" + suffix + "@" + domain.GuestEmailDomain; user.Email != want {
		t.Errorf("Email = %q, want %q", user.Email, want)
	}
	if stored, ok := users.Users[user.ID]; !ok || stored != user {
		t.Error("guest user was not stored via UserRepository.Create")
	}

	if sessionID == "" {
		t.Fatal("sessionID is empty")
	}
	if len(auth.created) != 1 {
		t.Fatalf("sessions created = %d, want 1", len(auth.created))
	}
	session := auth.created[0]
	if session.ID != sessionID {
		t.Errorf("session.ID = %q, want returned id %q", session.ID, sessionID)
	}
	if session.UserID != user.ID {
		t.Errorf("session.UserID = %d, want %d", session.UserID, user.ID)
	}
	if want := guestTestNow.Add(90 * time.Minute); !session.ExpiresAt.Equal(want) {
		t.Errorf("session.ExpiresAt = %v, want now + GuestSessionTTL = %v", session.ExpiresAt, want)
	}
	if got, err := auth.GetSessionUser(context.Background(), sessionID); err != nil || got.ID != user.ID {
		t.Errorf("GetSessionUser(%q) = %v, %v; want user %d", sessionID, got, err, user.ID)
	}
}

func TestAuthService_GuestLogin_EachCallIsANewGuest(t *testing.T) {
	users := testutil.NewUserRepo()
	auth := &sessionRecorder{AuthRepo: testutil.NewAuthRepo()}
	svc := newGuestAuthService(users, auth)

	first, firstSession, err := svc.GuestLogin(context.Background())
	if err != nil {
		t.Fatalf("first GuestLogin() error = %v", err)
	}
	second, secondSession, err := svc.GuestLogin(context.Background())
	if err != nil {
		t.Fatalf("second GuestLogin() error = %v", err)
	}
	if first.ID == second.ID {
		t.Errorf("both guests got user ID %d", first.ID)
	}
	if first.GoogleID == second.GoogleID || first.Email == second.Email {
		t.Errorf("guests share identity: %q/%q and %q/%q", first.GoogleID, first.Email, second.GoogleID, second.Email)
	}
	if firstSession == secondSession {
		t.Errorf("both guests got session %q", firstSession)
	}
	for sessionID, want := range map[string]uint{firstSession: first.ID, secondSession: second.ID} {
		got, err := auth.GetSessionUser(context.Background(), sessionID)
		if err != nil || got.ID != want {
			t.Errorf("GetSessionUser(%q) = %v, %v; want user %d", sessionID, got, err, want)
		}
	}
}

func TestAuthService_GuestLogin_PropagatesUserRepoError(t *testing.T) {
	repoErr := errors.New("db down")
	users := testutil.NewUserRepo()
	users.CreateFn = func(context.Context, *models.User) error { return repoErr }
	auth := &sessionRecorder{AuthRepo: testutil.NewAuthRepo()}
	svc := newGuestAuthService(users, auth)

	user, sessionID, err := svc.GuestLogin(context.Background())
	if !errors.Is(err, repoErr) {
		t.Fatalf("error = %v, want %v", err, repoErr)
	}
	if user != nil || sessionID != "" {
		t.Errorf("GuestLogin() = %v, %q; want nil, empty", user, sessionID)
	}
	if len(auth.created) != 0 {
		t.Errorf("sessions created = %d, want 0 when the user could not be created", len(auth.created))
	}
}

func TestAuthService_GuestLogin_PropagatesSessionError(t *testing.T) {
	sessionErr := errors.New("session insert failed")
	auth := &sessionRecorder{AuthRepo: testutil.NewAuthRepo(), err: sessionErr}
	svc := newGuestAuthService(testutil.NewUserRepo(), auth)

	user, sessionID, err := svc.GuestLogin(context.Background())
	if !errors.Is(err, sessionErr) {
		t.Fatalf("error = %v, want %v", err, sessionErr)
	}
	if user != nil || sessionID != "" {
		t.Errorf("GuestLogin() = %v, %q; want nil, empty", user, sessionID)
	}
}

func TestNewAuthService_DefaultsClockToTimeNow(t *testing.T) {
	svc := NewAuthService(testutil.NewUserRepo(), testutil.NewAuthRepo(), &config.Config{})
	if svc.now == nil {
		t.Fatal("now is nil, want time.Now")
	}
	if d := time.Since(svc.now()); d < 0 || d > time.Minute {
		t.Errorf("now() is %v away from time.Now", d)
	}
}

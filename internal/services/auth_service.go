package services

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"backend/internal/config"
	"backend/internal/domain"
	"backend/internal/models"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

type GoogleProfile struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	EmailVerified bool   `json:"verified_email"`
}

type AuthService struct {
	users      domain.UserRepository
	auth       domain.AuthRepository
	google     oauth2.Config
	stateTTL   time.Duration
	sessionTTL time.Duration
}

func NewAuthService(users domain.UserRepository, auth domain.AuthRepository, cfg *config.Config) *AuthService {
	return &AuthService{
		users: users, auth: auth,
		google: oauth2.Config{
			ClientID: cfg.Auth.GoogleClientID, 
			ClientSecret: cfg.Auth.GoogleClientSecret,
			RedirectURL: cfg.Auth.GoogleRedirectURL,
			Endpoint:    oauth2.Endpoint{AuthURL: "https://accounts.google.com/o/oauth2/auth", TokenURL: "https://oauth2.googleapis.com/token"},
			Scopes:      []string{"openid", "email", "profile"},
		},
		stateTTL: cfg.Auth.OAuthStateTTL, sessionTTL: cfg.Auth.SessionTTL,
	}
}

func (s *AuthService) LoginURL(ctx context.Context) (string, string, error) {
	state := uuid.NewString()
	verifier, err := randomToken(32)
	if err != nil {
		return "", "", fmt.Errorf("generate PKCE verifier: %w", err)
	}
	if err := s.auth.SaveOAuthState(ctx, &models.OAuthState{State: state, PKCEVerifier: verifier, ExpiresAt: time.Now().Add(s.stateTTL)}); err != nil {
		return "", "", fmt.Errorf("save OAuth state: %w", err)
	}
	return s.google.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(verifier)), state, nil
}

func (s *AuthService) Callback(ctx context.Context, state, code string) (*models.User, string, error) {
	record, err := s.auth.ConsumeOAuthState(ctx, state)
	if err != nil {
		return nil, "", fmt.Errorf("consume OAuth state: %w", err)
	}
	token, err := s.google.Exchange(ctx, code, oauth2.VerifierOption(record.PKCEVerifier))
	if err != nil {
		return nil, "", fmt.Errorf("exchange Google code: %w", err)
	}
	profile, err := fetchGoogleProfile(ctx, s.google.Client(ctx, token))
	if err != nil {
		return nil, "", err
	}
	if profile.ID == "" || profile.Email == "" || !profile.EmailVerified {
		return nil, "", fmt.Errorf("Google account email is missing or unverified")
	}
	user, err := s.users.UpsertGoogle(ctx, &models.User{GoogleID: profile.ID, Email: profile.Email, Name: profile.Name, Role: "member"})
	if err != nil {
		return nil, "", fmt.Errorf("upsert user: %w", err)
	}
	sessionID := uuid.NewString()
	if err := s.auth.CreateSession(ctx, &models.Session{ID: sessionID, UserID: user.ID, ExpiresAt: time.Now().Add(s.sessionTTL)}); err != nil {
		return nil, "", fmt.Errorf("create session: %w", err)
	}
	return user, sessionID, nil
}

func (s *AuthService) Logout(ctx context.Context, sessionID string) error {
	return s.auth.DeleteSession(ctx, sessionID)
}

func fetchGoogleProfile(ctx context.Context, client *http.Client) (*GoogleProfile, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch Google profile: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Google profile returned status %d", response.StatusCode)
	}
	var profile GoogleProfile
	if err := json.NewDecoder(response.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("decode Google profile: %w", err)
	}
	return &profile, nil
}

func randomToken(size int) (string, error) {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", bytes), nil
}

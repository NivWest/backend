package services

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"backend/internal/domain"
)

var displayNameRegex = regexp.MustCompile(`^[A-Za-z0-9_-]{3,20}$`)

type UserService struct {
	repo domain.UserRepository
	now  func() time.Time
}

func NewUserService(repo domain.UserRepository) *UserService {
	return &UserService{repo: repo, now: time.Now}
}

// UpdateProfile updates the user's display name and/or leaderboard opt-in status (story NW-13).
func (s *UserService) UpdateProfile(ctx context.Context, userID uint, req domain.UpdateProfileRequest) (*domain.ProfileResponse, error) {
	user, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	var displayName *string
	if req.DisplayName != nil {
		name := strings.TrimSpace(*req.DisplayName)
		if !displayNameRegex.MatchString(name) {
			return nil, domain.ErrInvalidDisplayName
		}

		existing, err := s.repo.GetByDisplayName(ctx, name)
		if err == nil && existing.ID != userID {
			return nil, domain.ErrDisplayNameTaken
		}
		if err != nil && !errors.Is(err, domain.ErrUserNotFound) {
			return nil, err
		}
		displayName = &name
	}

	var optIn *bool
	var optedInAt *time.Time
	if req.LeaderboardOptIn != nil {
		optIn = req.LeaderboardOptIn
		if *optIn {
			effectiveName := ""
			if displayName != nil {
				effectiveName = *displayName
			} else if user.DisplayName != nil {
				effectiveName = *user.DisplayName
			}
			if effectiveName == "" {
				return nil, domain.ErrDisplayNameRequired
			}

			if !user.LeaderboardOptIn || user.OptedInAt == nil {
				t := s.now()
				optedInAt = &t
			}
		}
	}

	updated, err := s.repo.UpdateProfile(ctx, userID, displayName, optIn, optedInAt)
	if err != nil {
		return nil, err
	}

	resp := domain.NewProfileResponse(updated)
	return &resp, nil
}

func (s *UserService) DeleteUser(ctx context.Context, id uint) error {
	return s.repo.Delete(ctx, id)
}

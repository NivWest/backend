package services_test

import (
	"context"
	"errors"
	"testing"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/services"
	"backend/internal/testutil"
)

func TestUserService_UpdateProfile(t *testing.T) {
	u1 := &models.User{ID: 1, Name: "Guest 1", Email: "g1@test.invalid"}
	u2 := &models.User{ID: 2, Name: "Guest 2", Email: "g2@test.invalid"}

	repo := testutil.NewUserRepo(u1, u2)
	svc := services.NewUserService(repo)

	ctx := context.Background()

	t.Run("valid display name and opt in succeeds", func(t *testing.T) {
		name := "bull_42"
		optIn := true
		resp, err := svc.UpdateProfile(ctx, 1, domain.UpdateProfileRequest{
			DisplayName:      &name,
			LeaderboardOptIn: &optIn,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.DisplayName == nil || *resp.DisplayName != "bull_42" {
			t.Errorf("want display_name bull_42, got %v", resp.DisplayName)
		}
		if !resp.LeaderboardOptIn {
			t.Errorf("want leaderboard_opt_in true")
		}
	})

	t.Run("conflict on case-insensitive display name", func(t *testing.T) {
		name := "BULL_42"
		optIn := true
		_, err := svc.UpdateProfile(ctx, 2, domain.UpdateProfileRequest{
			DisplayName:      &name,
			LeaderboardOptIn: &optIn,
		})
		if !errors.Is(err, domain.ErrDisplayNameTaken) {
			t.Errorf("want ErrDisplayNameTaken for BULL_42, got %v", err)
		}
	})

	t.Run("validation error on too short display name", func(t *testing.T) {
		name := "x"
		optIn := true
		_, err := svc.UpdateProfile(ctx, 2, domain.UpdateProfileRequest{
			DisplayName:      &name,
			LeaderboardOptIn: &optIn,
		})
		if !errors.Is(err, domain.ErrInvalidDisplayName) {
			t.Errorf("want ErrInvalidDisplayName for 'x', got %v", err)
		}
	})

	t.Run("validation error on invalid characters", func(t *testing.T) {
		name := "bad name!"
		optIn := true
		_, err := svc.UpdateProfile(ctx, 2, domain.UpdateProfileRequest{
			DisplayName:      &name,
			LeaderboardOptIn: &optIn,
		})
		if !errors.Is(err, domain.ErrInvalidDisplayName) {
			t.Errorf("want ErrInvalidDisplayName for 'bad name!', got %v", err)
		}
	})

	t.Run("validation error on opt in without display name", func(t *testing.T) {
		optIn := true
		_, err := svc.UpdateProfile(ctx, 2, domain.UpdateProfileRequest{
			LeaderboardOptIn: &optIn,
		})
		if !errors.Is(err, domain.ErrDisplayNameRequired) {
			t.Errorf("want ErrDisplayNameRequired, got %v", err)
		}
	})

	t.Run("opt out succeeds", func(t *testing.T) {
		optIn := false
		resp, err := svc.UpdateProfile(ctx, 1, domain.UpdateProfileRequest{
			LeaderboardOptIn: &optIn,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.LeaderboardOptIn {
			t.Errorf("want leaderboard_opt_in false")
		}
		if resp.DisplayName == nil || *resp.DisplayName != "bull_42" {
			t.Errorf("display name should be preserved, got %v", resp.DisplayName)
		}
	})
}

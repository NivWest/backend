package domain

import "errors"

var (
	ErrUserAlreadyExists          = errors.New("user already exists")
	ErrUserNotFound               = errors.New("user not found")
	ErrDisplayNameTaken           = errors.New("display name already taken")
	ErrInvalidDisplayName         = errors.New("invalid display name")
	ErrDisplayNameRequired        = errors.New("display name is required to join leaderboard")
	ErrInvalidMetric              = errors.New("invalid leaderboard metric")
	ErrLeaderboardEntryNotFound   = errors.New("leaderboard entry not found")
)

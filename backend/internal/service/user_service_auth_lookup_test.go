package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type avatarCountingUserRepo struct {
	UserRepository
	avatarReads int
}

func (r *avatarCountingUserRepo) GetByID(_ context.Context, id int64) (*User, error) {
	return &User{ID: id, Status: StatusActive}, nil
}

func (r *avatarCountingUserRepo) GetUserAvatar(context.Context, int64) (*UserAvatar, error) {
	r.avatarReads++
	return nil, nil
}

func TestGetByIDForAuthSkipsAvatar(t *testing.T) {
	repo := &avatarCountingUserRepo{}
	svc := &UserService{userRepo: repo}

	user, err := svc.GetByIDForAuth(context.Background(), 7)
	require.NoError(t, err)
	require.EqualValues(t, 7, user.ID)
	require.Zero(t, repo.avatarReads, "authentication must not load the avatar")

	_, err = svc.GetByID(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, 1, repo.avatarReads, "GetByID still hydrates the avatar for profile reads")
}

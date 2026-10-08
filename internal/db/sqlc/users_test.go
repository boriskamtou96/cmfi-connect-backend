package db

import (
	"context"
	"database/sql"
	"testing"

	"github.com/go-faker/faker/v4"
	"github.com/stretchr/testify/require"
)

func createRandomUser(t *testing.T) User {
	params := RegisterUserParams{
		FirstName: faker.FirstName(),
		LastName: sql.NullString{
			String: faker.LastName(),
			Valid:  true,
		},
		// phone_number is UNIQUE: each test user needs its own
		PhoneNumber:  faker.E164PhoneNumber(),
		HashPassword: []byte(faker.Password()),
	}

	user, err := testQueries.RegisterUser(context.Background(), params)
	require.NoError(t, err)
	require.NotEmpty(t, user)

	require.Equal(t, user.FirstName, params.FirstName)
	require.Equal(t, user.LastName, params.LastName)
	require.Equal(t, user.LastName, params.LastName)

	require.NotZero(t, user.ID)
	require.NotZero(t, user.CreatedAt)

	return user
}

func TestRegisterUser(t *testing.T) {
	createRandomUser(t)
}

func TestGetUserById(t *testing.T) {
	userID := 1

	user, err := testQueries.GetUserById(context.Background(), int64(userID))
	require.NoError(t, err)
	require.NotEmpty(t, user)

	require.Equal(t, user.FirstName, "KAMTOU")
	require.Equal(t, user.LastName, sql.NullString{String: "Boris", Valid: true})
}

func TestListUsers(t *testing.T) {
	arg := ListUsersParams{
		Limit:  10,
		Offset: 0,
	}

	users, err := testQueries.ListUsers(context.Background(), arg)
	require.NoError(t, err)
	require.NotEmpty(t, users)

	require.NotEqual(t, len(users), 0)
}

func TestUpdateUser(t *testing.T) {
	user := createRandomUser(t)
	arg := UpdateUserParams{
		ID:           user.ID,
		FirstName:    "CORDIA",
		LastName:     user.LastName,
		HashPassword: user.HashPassword,
	}
	updatedUser, err := testQueries.UpdateUser(context.Background(), arg)
	require.NoError(t, err)

	require.NotEmpty(t, updatedUser)
	require.Equal(t, user.FirstName, "CORDIA")
}

func TestDeleteUser(t *testing.T) {
	userID := 6

	err := testQueries.DeleteUser(context.Background(), int64(userID))
	require.NoError(t, err)
}

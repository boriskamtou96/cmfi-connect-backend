package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func createUserProfile(t *testing.T) Profile {
	params := CreateProfileParams{
		UserID: int64(23),
		BirthDate: sql.NullTime{
			Time: time.Date(
				1996,
				time.March,
				15,
				0,
				0,
				0,
				0,
				time.UTC,
			),
			Valid: true,
		},
		City: sql.NullString{
			String: "Yaoundé",
			Valid:  true,
		},
		Country: sql.NullString{
			String: "Cameroun",
			Valid:  true,
		},
		Church: sql.NullString{
			String: "PSU-Yaoundé",
			Valid:  true,
		},
		Assembly: sql.NullString{
			String: "Chez les KAMTOU",
			Valid:  true,
		},
	}

	profile, err := testQueries.CreateProfile(context.Background(), params)
	require.NoError(t, err)
	require.NotEmpty(t, profile)

	require.Equal(t, profile.UserID, int64(23))

	return profile
}

func TestCreateProfile(t *testing.T) {
	createUserProfile(t)
}

func TestGetUserProfile(t *testing.T) {
	profile, err := testQueries.GetProfile(context.Background(), int64(2))
	require.NoError(t, err)
	require.NotEmpty(t, profile)

	require.Equal(t, profile.UserID, int64(22))
}

func TestUpdateProfile(t *testing.T) {

	params := UpdateProfileParams{
		ID: int64(5),
		BirthDate: sql.NullTime{
			Time: time.Date(
				1995,
				time.January,
				20,
				0,
				0,
				0,
				0,
				time.UTC,
			),
			Valid: true,
		},
		City: sql.NullString{
			String: "Douala",
			Valid:  true,
		},
		Country: sql.NullString{
			String: "Cameroun",
			Valid:  true,
		},
		Church: sql.NullString{
			String: "CMCI",
			Valid:  true,
		},
		Assembly: sql.NullString{
			String: "Bonamoussadi",
			Valid:  true,
		},
	}

	updatedProfile, err := testQueries.UpdateProfile(
		context.Background(),
		params,
	)

	require.NoError(t, err)
	require.NotEmpty(t, updatedProfile)

	require.Equal(t, "Douala", updatedProfile.City.String)
	require.True(t, updatedProfile.City.Valid)

	require.Equal(t, "Cameroun", updatedProfile.Country.String)
	require.True(t, updatedProfile.Country.Valid)

	require.Equal(t, "CMCI", updatedProfile.Church.String)
	require.True(t, updatedProfile.Church.Valid)

	require.Equal(t, "Bonamoussadi", updatedProfile.Assembly.String)
	require.True(t, updatedProfile.Assembly.Valid)
}

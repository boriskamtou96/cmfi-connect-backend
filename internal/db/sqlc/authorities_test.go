package db

import (
	"context"
	"database/sql"
	"testing"

	"github.com/go-faker/faker/v4"
	"github.com/stretchr/testify/require"
)

func createAuthority(t *testing.T) Authority {
	params := CreateUserAuthorityParams{
		FirstName: faker.FirstName(),
		LastName: sql.NullString{
			String: faker.LastName(),
			Valid:  true,
		},
		PhoneNumber: sql.NullString{String: faker.Phonenumber(), Valid: true},
		Email: sql.NullString{
			String: faker.Email(),
			Valid:  false,
		},
		IsDiscipleMaker: true,
		// its own user: the test doesn't depend on the data of the database
		UserID: createRandomUser(t).ID,
		Status: "accepted",
	}

	authority, err := testQueries.CreateUserAuthority(context.Background(), params)
	require.NoError(t, err)
	require.NotEmpty(t, authority)

	require.Equal(t, authority.FirstName, params.FirstName)
	require.Equal(t, authority.LastName, params.LastName)
	require.Equal(t, authority.LastName, params.LastName)

	return authority
}

func TestCreateUserAuthority(t *testing.T) {
	createAuthority(t)
}

func TestGetUserAuthorities(t *testing.T) {
	params := GetUserAuthoritiesParams{
		UserID: 22,
		Limit:  10,
		Offset: 0,
	}
	authorities, err := testQueries.GetUserAuthorities(context.Background(), params)
	require.NoError(t, err)
	require.NotEmpty(t, authorities)

	require.Equal(t, authorities[1].LastName, sql.NullString{String: "", Valid: false})
	require.NotZero(t, len(authorities))
	require.Equal(t, len(authorities), 2)
}

func TestGetAuthorityByID(t *testing.T) {
	ID := 3

	authority, err := testQueries.GetAuthorityById(context.Background(), int64(ID))
	require.NoError(t, err)

	require.NotEmpty(t, authority)
	require.Equal(t, authority.ID, int64(1))
}

func TestDeleteAuthority(t *testing.T) {
	params := DeleteAuthorityParams{
		ID:     1,
		UserID: 22,
	}

	err := testQueries.DeleteAuthority(context.Background(), params)
	require.NoError(t, err)

	_, err = testQueries.GetAuthorityById(context.Background(), int64(4))
	require.Error(t, err)
}

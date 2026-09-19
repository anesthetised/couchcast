package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=4$"))

	ok, err := VerifyPassword(hash, "correct horse battery staple")
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = VerifyPassword(hash, "wrong")
	require.NoError(t, err)
	assert.False(t, ok)

	other, err := HashPassword("correct horse battery staple")
	require.NoError(t, err)
	assert.NotEqual(t, hash, other, "salts differ")

	_, err = VerifyPassword("$bcrypt$nope", "x")
	assert.ErrorIs(t, err, ErrInvalidHash)
	_, err = VerifyPassword("$argon2id$v=19$m=1,t=1,p=1$!!!$!!!", "x")
	assert.ErrorIs(t, err, ErrInvalidHash)
}

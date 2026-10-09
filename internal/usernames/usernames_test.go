package usernames

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalize(t *testing.T) {
	require.Equal(t, "boris.kamtou", Normalize("  @Boris.Kamtou "))
}

func TestCheck(t *testing.T) {
	for _, valid := range []string{"bob", "boris.kamtou", "b_k", "jean2", strings.Repeat("a", 30)} {
		require.Equal(t, Problem(""), Check(valid), valid)
	}
	for _, invalid := range []string{
		"bo", strings.Repeat("a", 31), ".bob", "bob.", "_bob", "bo..b", "bo._b",
		"bo b", "bob-k", "Bob", "éric",
	} {
		require.Equal(t, Invalid, Check(invalid), invalid)
	}
	for _, reserved := range []string{"admin", "cmfi", "cmfi.connect", "c.m.f.i", "support"} {
		require.Equal(t, Reserved, Check(reserved), reserved)
	}
}

func TestCandidatesComeFromTheName(t *testing.T) {
	candidates := Candidates("Boris", "Kamtou")
	require.Equal(t, []string{"boris.kamtou", "boriskamtou", "boris_kamtou", "kamtou.boris", "boris.k", "borisk", "boris"}, candidates[:7])
	require.Contains(t, candidates, "boris.kamtou237")
	for _, candidate := range candidates {
		require.Equal(t, Problem(""), Check(candidate), candidate)
	}
}

func TestCandidatesWithoutAccentsNorSigns(t *testing.T) {
	require.Equal(t, "eleonore.nguessan", Candidates("Éléonore", "N'Guessan")[0])
	require.Equal(t, "jeanmarc.lefevre", Candidates("Jean-Marc", "Lefèvre")[0])
	require.Equal(t, "coeur", Candidates("Cœur", "")[0])
}

func TestCandidatesForShortOrMissingNames(t *testing.T) {
	require.Equal(t, "membre", Candidates("", "")[0])
	require.Equal(t, "kamtou", Candidates("", "Kamtou")[0])
	// "al" alone is too short: lengthened
	require.Contains(t, Candidates("Al", ""), "al1")

	long := Candidates(strings.Repeat("a", 20), strings.Repeat("b", 20))
	for _, candidate := range long {
		require.LessOrEqual(t, len(candidate), MaxLength)
		require.Equal(t, Problem(""), Check(candidate), candidate)
	}
}

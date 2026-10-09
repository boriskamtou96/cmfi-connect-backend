// Package usernames holds the rules of the members' pseudos: 3 to 30
// characters among a-z, 0-9, "." and "_", starting and ending with a letter or
// a digit, unique without regard to case, some words kept for the community.
package usernames

import (
	"regexp"
	"strings"
)

const (
	MinLength = 3
	MaxLength = 30
)

// Why a pseudo can't be used.
type Problem string

const (
	Invalid  Problem = "invalid"
	Reserved Problem = "reserved"
	Taken    Problem = "taken"
)

var format = regexp.MustCompile(`^[a-z0-9][a-z0-9._]{1,28}[a-z0-9]$`)

// Words that could pass for the community or its team.
var reserved = map[string]bool{
	"admin": true, "administrateur": true, "administrator": true, "superadmin": true,
	"root": true, "system": true, "systeme": true, "api": true, "null": true,
	"support": true, "aide": true, "help": true, "contact": true, "info": true,
	"cmfi": true, "cmficonnect": true, "cmfi.connect": true, "cmfi_connect": true,
	"moderateur": true, "moderator": true, "staff": true, "team": true, "equipe": true,
	"officiel": true, "official": true, "pasteur": true, "pastor": true,
}

// Normalize gives the stored form: no spaces around, no leading "@", lowercase.
func Normalize(username string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(username), "@"))
}

// Check tells why a normalized pseudo can't be used ("" when its form is fine;
// whether it is free is for the database to say).
func Check(username string) Problem {
	if !format.MatchString(username) ||
		strings.Contains(username, "..") || strings.Contains(username, "__") ||
		strings.Contains(username, "._") || strings.Contains(username, "_.") {
		return Invalid
	}
	if reserved[username] || reserved[strings.NewReplacer(".", "", "_", "").Replace(username)] {
		return Reserved
	}
	return ""
}

// Accented letters of French (and close languages) and their plain form.
var plain = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a", "á", "a", "ã", "a", "å", "a",
	"ç", "c", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i", "ñ", "n",
	"ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u", "ý", "y", "ÿ", "y",
	"œ", "oe", "æ", "ae",
)

// slug keeps the letters and digits of a name, without accents: "Jean-Marc" -> "jeanmarc".
func slug(name string) string {
	var b strings.Builder
	for _, r := range plain.Replace(strings.ToLower(name)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Candidates are pseudos made from a name, the most natural first. Each one has
// the right form; the caller removes those already taken.
func Candidates(firstName, lastName string) []string {
	first, last := slug(firstName), slug(lastName)
	if first == "" {
		first, last = last, ""
	}
	if first == "" {
		first = "membre"
	}

	var raw []string
	if last != "" {
		raw = append(raw,
			first+"."+last,
			first+last,
			first+"_"+last,
			last+"."+first,
			first+"."+last[:1],
			first+last[:1],
		)
	}
	raw = append(raw, first)
	// then numbered, for common names
	base := raw[0]
	for _, n := range []string{"2", "3", "7", "10", "12", "21", "33", "77", "237"} {
		raw = append(raw, base+n)
	}

	seen := map[string]bool{}
	var candidates []string
	for _, candidate := range raw {
		candidate = fit(candidate)
		if !seen[candidate] && Check(candidate) == "" {
			seen[candidate] = true
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

// fit cuts a candidate to the maximum length, without a "." or "_" at the end,
// and lengthens one that is too short.
func fit(candidate string) string {
	if len(candidate) > MaxLength {
		candidate = candidate[:MaxLength]
	}
	candidate = strings.TrimRight(candidate, "._")
	for len(candidate) < MinLength {
		candidate += "1"
	}
	return candidate
}

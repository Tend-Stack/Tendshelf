package users

import (
	"net/mail"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// NewUser is the input for creating an account.
type NewUser struct {
	Username, DisplayName, Email, Password string
	Role                                   Role
}

// FieldErrors maps a form field to a short error code. The web layer turns
// codes into words; this package never writes interface copy.
type FieldErrors map[string]string

// Error lists the fields in order, for logs and tests.
func (e FieldErrors) Error() string {
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + ": " + e[k]
	}
	return "users: invalid fields (" + strings.Join(parts, ", ") + ")"
}

var usernameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

// Normalize trims the text fields and lowercases the username. The password
// is left exactly as typed.
func Normalize(n NewUser) NewUser {
	n.Username = strings.ToLower(strings.TrimSpace(n.Username))
	n.DisplayName = strings.TrimSpace(n.DisplayName)
	n.Email = strings.TrimSpace(n.Email)
	return n
}

// Validate checks a normalised NewUser and returns the problems by field, or
// nil when it is fine.
func Validate(n NewUser) FieldErrors {
	errs := FieldErrors{}
	switch {
	case strings.IndexFunc(n.DisplayName, unicode.IsControl) >= 0:
		errs["display_name"] = "invalid"
	case n.DisplayName == "":
		errs["display_name"] = "required"
	case utf8.RuneCountInString(n.DisplayName) > 80:
		errs["display_name"] = "too_long"
	}
	if !usernameRE.MatchString(n.Username) {
		errs["username"] = "invalid"
	}
	if n.Email != "" && !ValidEmail(n.Email) {
		errs["email"] = "invalid"
	}
	switch pl := utf8.RuneCountInString(n.Password); {
	case pl < 10:
		errs["password"] = "too_short"
	case pl > 256:
		errs["password"] = "too_long"
	case strings.EqualFold(n.Password, n.Username):
		errs["password"] = "same_as_username"
	}
	if len(errs) == 0 {
		return nil
	}
	return errs
}

// ValidEmail reports whether s (already trimmed) is a plain address of at most 254 bytes.
func ValidEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && len(s) <= 254
}

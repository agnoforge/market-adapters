package domain

import "fmt"

// Name is the identity of a Composite Dataset: a kebab-case slug, unique
// across the context. It is the primary key, and it deliberately leaves room
// for a later `name@version` identity — which is why "@" is not a legal
// character in one.
type Name string

// maxNameLength bounds a Name so a slug stays a label and not a document.
const maxNameLength = 64

// ParseName accepts a kebab-case slug — lowercase letters and digits in
// groups separated by single hyphens, no leading, trailing or doubled hyphen —
// and rejects everything else with an error wrapping ErrInvalidConfig.
func ParseName(s string) (Name, error) {
	if !isSlug(s) || len(s) > maxNameLength {
		return "", fmt.Errorf("%w: name %q is not a kebab-case slug", ErrInvalidConfig, s)
	}
	return Name(s), nil
}

// String returns the slug.
func (n Name) String() string { return string(n) }

// isSlug reports whether s is one or more groups of lowercase alphanumerics
// joined by single hyphens.
func isSlug(s string) bool {
	if s == "" {
		return false
	}
	group := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			group++
		case c == '-':
			if group == 0 {
				return false
			}
			group = 0
		default:
			return false
		}
	}
	return group > 0
}

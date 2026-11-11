package parental

import (
	"context"
	"errors"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// ErrApproveForbidden: a restricted account may approve a sign-in code only for
// itself.
var ErrApproveForbidden = errors.New("parental: a restricted account signs in only itself")

// ApproveAs says whose sign-in an approved device code becomes. asUser empty (or
// the caller) is the caller. Another user is allowed only when that user is
// restricted and the caller is their guardian or an admin: anything else is
// ErrNotRestricted (answered as missing, so an account's existence is not shown).
// A restricted caller naming anyone else is ErrApproveForbidden, so a child cannot
// bring an adult's account onto a device.
func (s *Store) ApproveAs(ctx context.Context, p *principal.Principal, asUser string) (string, error) {
	if p == nil {
		return "", ErrNotRestricted
	}
	if asUser == "" || asUser == p.UserID {
		return p.UserID, nil
	}
	restricted, err := s.IsRestricted(ctx, p.UserID)
	if err != nil {
		return "", err
	}
	if restricted {
		return "", ErrApproveForbidden
	}
	if _, err := s.Policy(ctx, asUser); err != nil {
		return "", err
	}
	access, err := s.AccessTo(ctx, p, asUser)
	if err != nil {
		return "", err
	}
	if access == AccessNone {
		return "", ErrNotRestricted
	}
	return asUser, nil
}

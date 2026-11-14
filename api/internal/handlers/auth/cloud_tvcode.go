package auth

// Epic 58 story 58.03: a TV's cloud code approved on maktaba.tv. The cloud account that approved
// it is linked to a user of this home; the cloud signed a one-use assertion naming that account,
// this home and the code (purpose tvcode), and handed it here through the relay. This finds the
// home user it stands for, with the same checks as a cloud sign-in.

import (
	"context"
	"errors"
	"fmt"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/cloudlink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/securityaudit"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	discoveryh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/discovery"
)

// TVCodeApprover is the home user a tvcode assertion approves code as: the user its cloud account
// is linked to, and that cloud account (the TV's sign-in is recorded only while the link still
// names it, RecordTVCodeSignIn). Every refusal wraps discoveryh.ErrCloudAssertionRefused (the reason is for the
// audit log, never the answer); discoveryh.ErrCloudNotLinked when this home holds no cloud
// identity.
func (h *Handler) TVCodeApprover(ctx context.Context, assertion, code string) (userID, cloudUserID string, err error) {
	if h.Cloud == nil || h.Cloud.Store == nil || h.Cloud.Identity == nil {
		return "", "", discoveryh.ErrCloudNotLinked
	}
	id, pub, err := h.Cloud.Identity.Load()
	if err != nil {
		return "", "", discoveryh.ErrCloudNotLinked
	}
	refuse := func(reason, userID string) (string, string, error) {
		h.audit(ctx, securityaudit.Entry{Event: securityaudit.EventLoginFailed, ActorUserID: userID,
			Payload: map[string]any{"via": "cloud_tv_code", "reason": reason}})
		return "", "", fmt.Errorf("%w: %s", discoveryh.ErrCloudAssertionRefused, reason)
	}
	a, err := cloudlink.VerifyAssertion(assertion, pub, cloudlink.PurposeTVCode, id.ServerID, h.now())
	if err != nil {
		return refuse(err.Error(), "")
	}
	if a.Non != code {
		// signed for another code: approving this one would be a different TV
		return refuse("assertion names another code", "")
	}
	if err := h.Cloud.Store.UseAssertion(ctx, a); err != nil {
		if errors.Is(err, cloudlink.ErrReplayed) {
			return refuse("assertion replayed", "")
		}
		return "", "", err
	}
	link, err := h.Cloud.Store.LinkByCloudUser(ctx, a.Sub)
	if errors.Is(err, cloudlink.ErrNotLinked) {
		return refuse("cloud account not linked", "")
	}
	if err != nil {
		return "", "", err
	}
	u, err := h.Users.GetByID(ctx, link.UserID)
	if errors.Is(err, users.ErrNotFound) || (err == nil && u.ID == users.SentinelAdminID) {
		return refuse("home user gone", link.UserID)
	}
	if err != nil {
		return "", "", err
	}
	if u.IsLocked(h.now()) {
		return refuse("home user locked", u.ID)
	}
	if u.MustChangePassword {
		return refuse("home user must change their password", u.ID)
	}
	return u.ID, link.CloudUserID, nil
}

// RecordTVCodeSignIn records the refresh family of a TV approved on maktaba.tv as a cloud sign-in
// of userID's link, scope play: unlinking the cloud account revokes it (Unlink), a refresh after
// the link is gone ends it, and its refreshed tokens are capped as any cloud sign-in's. It does so
// only while the link still names cloudUserID, the account that approved the TV, checked in the
// transaction that records it: a link removed, or changed to another account, since the approval
// revokes the family and returns discoveryh.ErrCloudLinkChanged (the TV gets no tokens).
func (h *Handler) RecordTVCodeSignIn(ctx context.Context, familyID, userID, cloudUserID string) error {
	if h.Cloud == nil || h.Cloud.Store == nil {
		return discoveryh.ErrCloudNotLinked
	}
	if familyID == "" {
		return errors.New("auth: no refresh family to record")
	}
	err := h.Cloud.Store.RecordLinkedSignIn(ctx, familyID, userID, cloudUserID, principal.CloudScopePlay)
	if errors.Is(err, cloudlink.ErrLinkChanged) {
		h.audit(ctx, securityaudit.Entry{Event: securityaudit.EventLoginFailed, ActorUserID: userID,
			Payload: map[string]any{"via": "cloud_tv_code", "reason": "cloud link removed or changed since the approval"}})
		return fmt.Errorf("%w: %w", discoveryh.ErrCloudLinkChanged, err)
	}
	return err
}

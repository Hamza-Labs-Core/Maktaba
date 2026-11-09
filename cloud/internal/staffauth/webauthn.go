package staffauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// Passkeys (story 50.33 §2, part 2): WebAuthn with the console's origin as the relying party,
// user verification required, attestation none. A counter that goes backwards means the
// passkey may have been copied: the handler disables it and alerts the owner.

// ChallengeTTL is how long a passkey ceremony may take, from its options to its answer.
const ChallengeTTL = 5 * time.Minute

// RPDisplayName is the name the browser shows when it asks for the passkey.
const RPDisplayName = "Maktaba console"

// NewWebAuthn is the relying party for the console at adminOrigin (server.admin_origin, for
// example https://admin.maktaba.tv): its rpId is the origin's host.
func NewWebAuthn(adminOrigin string) (*webauthn.WebAuthn, error) {
	u, err := url.Parse(adminOrigin)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("staffauth: admin origin %q is no web origin", adminOrigin)
	}
	timeout := webauthn.TimeoutConfig{Enforce: true, Timeout: ChallengeTTL, TimeoutUVD: ChallengeTTL}
	return webauthn.New(&webauthn.Config{
		RPID:          u.Hostname(),
		RPDisplayName: RPDisplayName,
		RPOrigins:     []string{u.Scheme + "://" + u.Host},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationRequired,
		},
		AttestationPreference: protocol.PreferNoAttestation,
		Timeouts:              webauthn.TimeoutsConfig{Login: timeout, Registration: timeout},
	})
}

// PasskeyUser is a staff account as WebAuthn sees it: its id as the user handle, its email as
// the name, and its enabled passkeys.
type PasskeyUser struct {
	ID          string
	Email       string
	Credentials []webauthn.Credential
}

func (u PasskeyUser) WebAuthnID() []byte                         { return []byte(u.ID) }
func (u PasskeyUser) WebAuthnName() string                       { return u.Email }
func (u PasskeyUser) WebAuthnDisplayName() string                { return u.Email }
func (u PasskeyUser) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }

// Exclusions are the user's passkeys as a registration's exclude list: the same authenticator
// is not registered twice.
func (u PasskeyUser) Exclusions() []protocol.CredentialDescriptor {
	out := make([]protocol.CredentialDescriptor, 0, len(u.Credentials))
	for i := range u.Credentials {
		out = append(out, u.Credentials[i].Descriptor())
	}
	return out
}

// DecodeCredential reads a stored credential record.
func DecodeCredential(raw []byte) (webauthn.Credential, error) {
	var c webauthn.Credential
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	if len(c.ID) == 0 || len(c.PublicKey) == 0 {
		return c, errors.New("staffauth: stored passkey has no id or key")
	}
	return c, nil
}

// EncodeCredential is a credential record for the store.
func EncodeCredential(c *webauthn.Credential) ([]byte, error) { return json.Marshal(c) }

// CounterWentBack is §2's check after a good signature: the authenticator's counter did not
// move forward (and is in use), so another copy of the key may exist.
func CounterWentBack(c *webauthn.Credential) bool { return c.Authenticator.CloneWarning }

// RegistrationOptions are a new passkey's options: user verification required, no
// attestation, a resident key preferred, and the user's passkeys excluded.
func RegistrationOptions(u PasskeyUser) []webauthn.RegistrationOption {
	return []webauthn.RegistrationOption{
		webauthn.WithExclusions(u.Exclusions()),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationRequired,
		}),
	}
}

// LoginOptions are an assertion's options: user verification required.
func LoginOptions() []webauthn.LoginOption {
	return []webauthn.LoginOption{webauthn.WithUserVerification(protocol.VerificationRequired)}
}

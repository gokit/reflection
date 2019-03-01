package nauth

import (
	"errors"
	"net/http"

	"github.com/gokit/npkg/nauth/sessions"
	"github.com/gokit/npkg/nxid"
)

// ErrNoCredentials is returned when giving claim fails to provide
// a credential.
var ErrNoCredentials = errors.New("Claim has no attached credentail")

// Credential defines what we expect from a custom implemented
// credential.
type Credential interface {
	Validate() error

	Type() string
	User() string
	Provider() string // google, email, phone, facebook, wechat, github, ...
}

// ClaimProvider defines what we expect from a Claim provider.
type ClaimProvider interface {
	EncodeClaim(Claim) ([]byte, error)
	ParseClaim(claim []byte) (Claim, error)
	ExtractClaim(r *http.Request) (Claim, error)
}

// Claims define what we expect from a Claim implementation.
type Claims interface {
	Valid() error
	HasRoles(...string) bool
	HasAnyRoles(...string) bool
}

// Claim defines authentication claims parsed from underline
// data provide to authenticator.
type Claim struct {
	Method string // jwt, user-password, oauth, ...
	Cred   Credential
}

// Valid returns an error if giving credentials could not be validated
// or if giving Claim has no attached credential.
func (c Claim) Valid() error {
	if c.Cred != nil {
		return c.Cred.Validate()
	}
	return ErrNoCredentials
}

// VerifiedClaim represents the response received back from the
// Authenticator as to a giving authenticated claim with associated
// session data.
type VerifiedClaim struct {
	User  nxid.ID
	Roles []string               // Roles of verified claim.
	Data  map[string]interface{} // Extra Data to be attached to session for user.
}

// Valid returns an error if giving credentials could not be validated
// or if giving Claim has no attached credential.
func (c VerifiedClaim) Valid() error {
	return nil
}

// HasRoles returns true/false if giving claim as all roles.
func (c VerifiedClaim) HasRoles(roles ...string) bool {
	for _, role := range roles {
		if c.checkRole(role) {
			continue
		}
		return false
	}
	return true
}

// HasAnyRoles returns true if giving claim as at least one roles.
func (c VerifiedClaim) HasAnyRoles(roles ...string) bool {
	for _, role := range roles {
		if c.checkRole(role) {
			return true
		}
	}
	return false
}

// ToSession returns a new sessions.Session instance from the verified claim.
func (c VerifiedClaim) ToSession() (sessions.Session, error) {
	var session sessions.Session
	return session, nil
}

// checkRole checks if any roles of Claim match provided.
func (c VerifiedClaim) checkRole(role string) bool {
	for _, myrole := range c.Roles {
		if myrole == role {
			return true
		}
	}
	return false
}

// Authenticator defines what we expect from a Authenticator of
// claims. It exposes the underline method used for verifying
// an authentication claim.
type Authenticator interface {
	// VerifyClaim exposes the underline function within Authenticator.Authenticate
	// used to authenticate the request claim and the returned verified claim. It
	// allow's testing and also
	VerifyClaim(Claim) (VerifiedClaim, error)
}

// AuthenticationProvider defines what the Authentication should be as,
// it both exposes the the method from Authenticator and the provides
// the Initiate and Authenticate methods which are the underline
// handlers of the initiation and finalization of requests to authenticate.
//
// Exposes such a final form allows us to swap in, any form of authentication
// be it email, facebook, google or oauth based without much work.
type AuthenticationProvider interface {
	Authenticator

	// Initiate handles the initial response to a request to initiate
	// a authentication procedure e.g to redirect to
	// a page for user-name and password login or google oauth page with
	// a secure token.
	Initiate(res http.ResponseWriter, req *http.Request)

	// Authenticate finalizes the response to finalize the authentication
	// process, which finalizes and verifies the authentication request
	// with a response as dictated by provider.
	//
	// The authenticate process can be the authentication of a new login
	// or the authentication of an existing login. The provider implementation
	// should decide for it'self as it sees fit to match on how this two should
	// be managed.
	Authenticate(res http.ResponseWriter, req *http.Request)

	// Verify exposes to others by the provider a means of getting a verified
	// claim from a incoming request after it's process of authentication.
	//
	// This lets others step into the middle of the Authentication procedure
	// to retrieve the verified request claim as dictated by provider, which
	// can be used for other uses.
	Verify(req *http.Request) (VerifiedClaim, error)
}

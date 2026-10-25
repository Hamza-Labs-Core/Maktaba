package cluster

import "errors"

// Why the home cannot give a machine a new login to its database (a re-made access: the
// home's push, or the machine's own request): the code the machine logs and the Machines
// page shows it by, with the reason in words.
const (
	// AccessNotApproved: the machine is not an approved member (pending, revoked).
	AccessNotApproved = "not_approved"
	// AccessUnknown: the home does not know the machine (removed, or never joined).
	AccessUnknown = "unknown"
	// AccessCannotCreate: the home's database user may not create roles, and the home
	// could not mend it (not its own bundled Postgres, or the mending failed).
	AccessCannotCreate = "cannot_create_roles"
	// AccessNoGroup: the machines' group role is missing and could not be made again.
	AccessNoGroup = "no_worker_group"
	// AccessNoAdmin: the home's database user may not change the machine's role (Postgres
	// 16: a role it does not administer, as one the superuser made in a restore), and the
	// home could not mend it.
	AccessNoAdmin = "cannot_alter_role"
	// AccessTerm: the cluster's term could not be read.
	AccessTerm = "term"
	// AccessCA: the bundled database's CA could not be read.
	AccessCA = "database_ca"
	// AccessRateLimited: the machine asked again too soon.
	AccessRateLimited = "rate_limited"
	// AccessNotTaken: the machine refused the access it was given (its answer in the
	// reason: a home of an earlier term, a switch under way).
	AccessNotTaken = "not_taken"
	// AccessOther: anything else (the database's error in the reason).
	AccessOther = "other"
)

// AccessError is a re-made access the home could not give, with its code.
type AccessError struct {
	Code   string
	Reason string
}

func (e *AccessError) Error() string { return e.Reason }

// AccessCode is err's access code (AccessOther for an error without one).
func AccessCode(err error) string {
	var ae *AccessError
	if errors.As(err, &ae) && ae.Code != "" {
		return ae.Code
	}
	return AccessOther
}

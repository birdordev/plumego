package identity

import (
	"errors"
)

var ErrForbidden = errors.New("permission denied")

type Scope struct {
	Tenant    string
	Project   string
	Principal string
}

type Permission string

const (
	View    Permission = "view"
	Execute Permission = "execute"
	Approve Permission = "approve"
	Manage  Permission = "manage"
)

type Role string

func (r Role) allows(permission Permission) bool {
	switch permission {
	case View:
		return r == "viewer" || r == "operator" || r == "approver" || r == "admin"
	case Execute:
		return r == "operator" || r == "admin"
	case Approve:
		return r == "approver"
	case Manage:
		return r == "admin"
	default:
		return false
	}
}

type Membership struct {
	Role     Role
	Revision int64
}

type AccountAccess struct {
	Provider           string
	RemoteIdentity     string
	CredentialRevision int64
	MembershipRevision int64
}

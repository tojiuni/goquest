package spicedb

// SpiceDB object types. goquest resources are namespaced with the goquest/ prefix
// to coexist with other services' definitions in the shared platform schema.
// The subject type `user` and the platform `org` type are shared (unprefixed).
const (
	TypeWorkspace = "goquest/workspace"
	TypeProject   = "goquest/project"
	TypeTicket    = "goquest/ticket"
	TypeUser      = "user" // shared across services; no goquest/ prefix
	TypeOrg       = "org"  // shared platform org; no goquest/ prefix
)

// Permission constants — passed to PermissionChecker.Check.
const (
	PermContractApprove = "contract_approve"
)

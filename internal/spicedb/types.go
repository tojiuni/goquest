package spicedb

// SpiceDB object types. goquest resources are namespaced with the goquest/ prefix
// to coexist with other services' definitions in the shared platform schema.
// The subject type `user` is shared (unprefixed).
const (
	TypeWorkspace = "goquest/workspace"
	TypeProject   = "goquest/project"
	TypeTicket    = "goquest/ticket"
	TypeUser      = "user"
)

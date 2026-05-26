package spicedb

import "encoding/base64"

// subjectID encodes a raw user identity into a SpiceDB-safe object id.
//
// SpiceDB object ids must match ^([a-zA-Z0-9/_|\-=+]+|\*)$. goquest user
// identities come from the JWT `sub` claim, which is currently an email
// (e.g. "lyckabc@toji.homes") containing `@` and `.` — both disallowed.
// base64.RawURLEncoding maps any byte string to [A-Za-z0-9-_] (no padding),
// which is SpiceDB-safe, deterministic, and collision-free.
//
// Applied centrally in touch/delete/check whenever the subject type is `user`,
// so the same raw identity always maps to the same subject across the writer,
// checker, and backfill.
func subjectID(userID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(userID))
}

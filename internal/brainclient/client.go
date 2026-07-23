package brainclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/tojiuni/goquest/internal/brainpb"
)

// ErrNoCallerJWT is returned when TicketDocs is called with no caller JWT to
// forward. brain requires a Bearer token on every RPC (BRAIN_AUTH_MODE=required)
// and goquest deliberately has no service-account fallback for this endpoint
// (spec §2.2, YAGNI) — callers without a session token get this error, not a
// brain round trip that would fail anyway.
var ErrNoCallerJWT = errors.New("brainclient: no caller JWT to forward")

// RelatedDoc is the minimum brain returns for a ticket's reference documents.
type RelatedDoc struct {
	Title      string
	SourcePath string
}

// Client wraps brain's GraphService for ticket-scoped doc lookups.
type Client struct {
	conn   *grpc.ClientConn
	graph  brainpb.GraphServiceClient
	tenant string
}

// New dials brain's gRPC endpoint (plaintext — in-cluster, same trust model
// as the existing SpiceDB client in internal/spicedb/client.go).
func New(addr, tenant string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("brainclient: dial %s: %w", addr, err)
	}
	return &Client{conn: conn, graph: brainpb.NewGraphServiceClient(conn), tenant: tenant}, nil
}

// Close closes the underlying gRPC connection.
func (c *Client) Close() error { return c.conn.Close() }

// TicketDocs resolves a ticket's reference documents via brain's TicketDocs
// RPC. callerJWT is the caller's own lymphhub-issued Bearer token (already
// verified by goquest's own auth middleware) — it is decoded here ONLY to
// derive the ReBAC `user` subject brain expects; the raw token is forwarded
// to brain verbatim so brain performs its own signature/exp/subject check
// (mirrors brain_mcp/src/callerAuth.ts's caller-JWT-forward pattern).
func (c *Client) TicketDocs(ctx context.Context, ticketID, callerJWT string) ([]RelatedDoc, error) {
	if callerJWT == "" {
		return nil, ErrNoCallerJWT
	}
	user, err := userFromJWT(callerJWT)
	if err != nil {
		return nil, fmt.Errorf("brainclient: decode caller JWT: %w", err)
	}

	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+callerJWT)
	resp, err := c.graph.TicketDocs(ctx, &brainpb.TicketDocsRequest{
		Tenant:   c.tenant,
		User:     user,
		TicketId: ticketID,
	})
	if err != nil {
		return nil, fmt.Errorf("brainclient: TicketDocs: %w", err)
	}

	docs := make([]RelatedDoc, 0, len(resp.Nodes))
	for _, n := range resp.Nodes {
		docs = append(docs, RelatedDoc{Title: n.Title, SourcePath: n.SourcePath})
	}
	return docs, nil
}

// userFromJWT decode-only parses the JWT payload (no signature verification —
// brain is the verifier) to build the ReBAC subject "user:<handle>". lymphhub
// tokens carry `sub` (an actor UUID) but never `preferred_username`
// (confirmed in brain's deploy/k8s/deployment.yaml comment) — preferred_username
// is preferred when present for forward-compatibility, matching
// brain_mcp/src/callerAuth.ts exactly.
func userFromJWT(raw string) (string, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", errors.New("malformed JWT")
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode JWT payload: %w", err)
	}
	var claims struct {
		Sub               string `json:"sub"`
		PreferredUsername string `json:"preferred_username"`
	}
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return "", fmt.Errorf("parse JWT payload: %w", err)
	}
	if claims.Sub == "" {
		return "", errors.New("JWT has no sub claim")
	}
	handle := claims.PreferredUsername
	if handle == "" {
		handle = claims.Sub
	}
	return "user:" + handle, nil
}

package spicedb

import (
	"context"
	"fmt"
	"strings"

	authzed "github.com/authzed/authzed-go/v1"
	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	"github.com/authzed/grpcutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client wraps the authzed SpiceDB permissions client.
type Client struct {
	inner *authzed.Client
}

// NewClient dials the SpiceDB endpoint and authenticates with the preshared token.
// Pass insecureDial=true for local/dev (plaintext); use TLS for prod.
func NewClient(endpoint, token string, insecureDial bool) (*Client, error) {
	var opts []grpc.DialOption
	if insecureDial {
		// Plaintext transport — the bearer token must be sent over the insecure
		// channel via WithInsecureBearerToken (WithBearerToken requires TLS).
		opts = append(opts,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpcutil.WithInsecureBearerToken(token),
		)
	} else {
		tlsOpt, err := grpcutil.WithSystemCerts(grpcutil.VerifyCA)
		if err != nil {
			return nil, fmt.Errorf("spicedb tls: %w", err)
		}
		opts = append(opts, tlsOpt, grpcutil.WithBearerToken(token))
	}

	c, err := authzed.NewClient(endpoint, opts...)
	if err != nil {
		return nil, fmt.Errorf("spicedb dial: %w", err)
	}
	return &Client{inner: c}, nil
}

// Checker returns a Checker bound to this client.
func (c *Client) Checker() *Checker { return &Checker{c: c} }

// Writer returns a RelationshipWriter bound to this client.
func (c *Client) Writer() *Writer { return &Writer{c: c} }

// HasTicketPermission reports whether the live SpiceDB schema defines permission
// perm on definition ticket. An error means the schema could not be read; callers
// treat that as "not present" (fail closed).
func (c *Client) HasTicketPermission(ctx context.Context, perm string) (bool, error) {
	resp, err := c.inner.ReadSchema(ctx, &v1.ReadSchemaRequest{})
	if err != nil {
		return false, err
	}
	return schemaDefinesPermission(resp.GetSchemaText(), "ticket", perm), nil
}

// schemaDefinesPermission scans schemaText for `definition <def> { ... }` and
// reports whether that block contains a line `permission <perm> ...`.
func schemaDefinesPermission(schemaText, def, perm string) bool {
	inDef := false
	for _, line := range strings.Split(schemaText, "\n") {
		fields := strings.Fields(line)
		if !inDef {
			inDef = len(fields) >= 2 && fields[0] == "definition" && fields[1] == def
			continue
		}
		if len(fields) == 1 && fields[0] == "}" {
			return false
		}
		if len(fields) >= 2 && fields[0] == "permission" && fields[1] == perm {
			return true
		}
	}
	return false
}

// HealthCheck verifies connectivity by reading the schema.
func (c *Client) HealthCheck(ctx context.Context) error {
	_, err := c.inner.ReadSchema(ctx, &v1.ReadSchemaRequest{})
	return err
}

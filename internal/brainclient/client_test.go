package brainclient

import (
	"context"
	"encoding/base64"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	"github.com/tojiuni/goquest/internal/brainpb"
)

const bufSize = 1024 * 1024

// fakeGraphService records the request it received (including incoming
// metadata) so tests can assert on what the client actually sent.
type fakeGraphService struct {
	brainpb.UnimplementedGraphServiceServer
	lastReq        *brainpb.TicketDocsRequest
	lastAuthHeader string
	response       *brainpb.TicketDocsResponse
	err            error
}

func (f *fakeGraphService) TicketDocs(ctx context.Context, req *brainpb.TicketDocsRequest) (*brainpb.TicketDocsResponse, error) {
	f.lastReq = req
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		vs := md.Get("authorization")
		if len(vs) > 0 {
			f.lastAuthHeader = vs[0]
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

// startFakeBrain runs fakeGraphService on an in-process bufconn listener and
// returns a dial function suitable for grpc.NewClient's WithContextDialer.
func startFakeBrain(t *testing.T, svc *fakeGraphService) func(context.Context, string) (net.Conn, error) {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	brainpb.RegisterGraphServiceServer(srv, svc)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return func(context.Context, string) (net.Conn, error) { return lis.Dial() }
}

// newTestClient builds a Client whose conn dials the given bufconn dialer
// instead of a real address — bypasses New()'s real-address dial so tests
// don't need a live brain.
func newTestClient(t *testing.T, dialer func(context.Context, string) (net.Conn, error), tenant string) *Client {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return &Client{conn: conn, graph: brainpb.NewGraphServiceClient(conn), tenant: tenant}
}

// makeJWT builds an unsigned test JWT (decode-only consumer — TicketDocs
// never verifies the signature, brain does) using the same unpadded
// base64url encoding real JWTs use.
func makeJWT(t *testing.T, sub, preferredUsername string) string {
	t.Helper()
	header := `{"alg":"none","typ":"JWT"}`
	payload := `{"sub":"` + sub + `"`
	if preferredUsername != "" {
		payload += `,"preferred_username":"` + preferredUsername + `"`
	}
	payload += `}`
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(header)) + "." + enc([]byte(payload)) + ".sig"
}

func TestTicketDocs_ForwardsAuthAndBuildsUserFromSub(t *testing.T) {
	svc := &fakeGraphService{response: &brainpb.TicketDocsResponse{
		Nodes: []*brainpb.RelatedNode{{Title: "Design Doc", SourcePath: "goquest/spec/foo.md"}},
	}}
	dialer := startFakeBrain(t, svc)
	c := newTestClient(t, dialer, "toji")

	jwt := makeJWT(t, "01ACTORID", "")
	docs, err := c.TicketDocs(context.Background(), "ticket-123", jwt)
	require.NoError(t, err)
	assert.Equal(t, []RelatedDoc{{Title: "Design Doc", SourcePath: "goquest/spec/foo.md"}}, docs)

	require.NotNil(t, svc.lastReq)
	assert.Equal(t, "toji", svc.lastReq.Tenant)
	assert.Equal(t, "ticket-123", svc.lastReq.TicketId)
	assert.Equal(t, "user:01ACTORID", svc.lastReq.User, "lymphhub tokens have no preferred_username — user must fall back to sub")
	assert.Equal(t, "Bearer "+jwt, svc.lastAuthHeader, "the raw JWT must be forwarded verbatim so brain can verify it")
}

func TestTicketDocs_PrefersPreferredUsernameOverSub(t *testing.T) {
	svc := &fakeGraphService{response: &brainpb.TicketDocsResponse{}}
	dialer := startFakeBrain(t, svc)
	c := newTestClient(t, dialer, "toji")

	jwt := makeJWT(t, "01ACTORID", "lyckabc")
	_, err := c.TicketDocs(context.Background(), "ticket-123", jwt)
	require.NoError(t, err)
	assert.Equal(t, "user:lyckabc", svc.lastReq.User)
}

func TestTicketDocs_NoCallerJWT_ReturnsErrWithoutCallingBrain(t *testing.T) {
	svc := &fakeGraphService{response: &brainpb.TicketDocsResponse{}}
	dialer := startFakeBrain(t, svc)
	c := newTestClient(t, dialer, "toji")

	_, err := c.TicketDocs(context.Background(), "ticket-123", "")
	require.ErrorIs(t, err, ErrNoCallerJWT)
	assert.Nil(t, svc.lastReq, "brain must not be called when there is no caller JWT")
}

func TestTicketDocs_EmptyResponse_ReturnsEmptySlice(t *testing.T) {
	svc := &fakeGraphService{response: &brainpb.TicketDocsResponse{Nodes: nil}}
	dialer := startFakeBrain(t, svc)
	c := newTestClient(t, dialer, "toji")

	docs, err := c.TicketDocs(context.Background(), "ticket-123", makeJWT(t, "01ACTORID", ""))
	require.NoError(t, err)
	assert.Empty(t, docs)
}

func TestTicketDocs_BrainError_Propagates(t *testing.T) {
	svc := &fakeGraphService{err: assertErr{}}
	dialer := startFakeBrain(t, svc)
	c := newTestClient(t, dialer, "toji")

	_, err := c.TicketDocs(context.Background(), "ticket-123", makeJWT(t, "01ACTORID", ""))
	require.Error(t, err)
}

type assertErr struct{}

func (assertErr) Error() string { return "brain unavailable" }

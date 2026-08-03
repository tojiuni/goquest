package cogitoclient

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/tojiuni/goquest/internal/cogitopb"
)

const bufSize = 1024 * 1024

// fakeCogitoService records the request it received so tests can assert on
// what the client actually sent.
type fakeCogitoService struct {
	cogitopb.UnimplementedCogitoServiceServer
	lastReq  *cogitopb.SplitContentRequest
	response *cogitopb.SplitContentResponse
	err      error
}

func (f *fakeCogitoService) SplitContent(_ context.Context, req *cogitopb.SplitContentRequest) (*cogitopb.SplitContentResponse, error) {
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

// startFakeCogito runs fakeCogitoService on an in-process bufconn listener
// and returns a dial function suitable for grpc.NewClient's WithContextDialer.
func startFakeCogito(t *testing.T, svc *fakeCogitoService) func(context.Context, string) (net.Conn, error) {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	cogitopb.RegisterCogitoServiceServer(srv, svc)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return func(context.Context, string) (net.Conn, error) { return lis.Dial() }
}

// newTestClient builds a Client whose conn dials the given bufconn dialer
// instead of a real address — bypasses New()'s real-address dial so tests
// don't need a live cogito.
func newTestClient(t *testing.T, dialer func(context.Context, string) (net.Conn, error)) *Client {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return &Client{conn: conn, cogito: cogitopb.NewCogitoServiceClient(conn)}
}

func TestSplitContent_MapsResponseIncludingNestedSubSections(t *testing.T) {
	svc := &fakeCogitoService{response: &cogitopb.SplitContentResponse{
		Toc: []string{"Intro", "Body"},
		Sections: []*cogitopb.Section{
			{
				Title:    "Intro",
				Contents: "intro text",
			},
			{
				Title:    "Body",
				Contents: "body text",
				SubSections: []*cogitopb.Section{
					{Title: "Body A", Contents: "body a text"},
					{
						Title:    "Body B",
						Contents: "body b text",
						SubSections: []*cogitopb.Section{
							{Title: "Body B.1", Contents: "body b1 text"},
						},
					},
				},
			},
		},
	}}
	dialer := startFakeCogito(t, svc)
	c := newTestClient(t, dialer)

	sections, toc, err := c.SplitContent(context.Background(), "some long text", "My Title")
	require.NoError(t, err)

	assert.Equal(t, []string{"Intro", "Body"}, toc)
	assert.Equal(t, []Section{
		{Title: "Intro", Contents: "intro text"},
		{
			Title:    "Body",
			Contents: "body text",
			SubSections: []Section{
				{Title: "Body A", Contents: "body a text"},
				{
					Title:    "Body B",
					Contents: "body b text",
					SubSections: []Section{
						{Title: "Body B.1", Contents: "body b1 text"},
					},
				},
			},
		},
	}, sections)

	require.NotNil(t, svc.lastReq)
	assert.Equal(t, "some long text", svc.lastReq.Text)
	assert.Equal(t, "My Title", svc.lastReq.Title)
}

func TestSplitContent_EmptyResponse_ReturnsNilSlices(t *testing.T) {
	svc := &fakeCogitoService{response: &cogitopb.SplitContentResponse{}}
	dialer := startFakeCogito(t, svc)
	c := newTestClient(t, dialer)

	sections, toc, err := c.SplitContent(context.Background(), "text", "")
	require.NoError(t, err)
	assert.Empty(t, sections)
	assert.Empty(t, toc)
}

func TestSplitContent_CogitoError_Propagates(t *testing.T) {
	svc := &fakeCogitoService{err: assertErr{}}
	dialer := startFakeCogito(t, svc)
	c := newTestClient(t, dialer)

	_, _, err := c.SplitContent(context.Background(), "text", "")
	require.Error(t, err)
}

type assertErr struct{}

func (assertErr) Error() string { return "cogito unavailable" }

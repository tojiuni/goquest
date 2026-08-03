package cogitoclient

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	"github.com/tojiuni/goquest/internal/cogitopb"
)

const bufSize = 1024 * 1024

// fakeCogitoService records the request it received so tests can assert on
// what the client actually sent.
type fakeCogitoService struct {
	cogitopb.UnimplementedCogitoServiceServer
	lastReq    *cogitopb.SplitContentRequest
	lastAuthMD string
	response   *cogitopb.SplitContentResponse
	err        error

	lastDecomposeReq  *cogitopb.DecomposeQuestionRequest
	decomposeResponse *cogitopb.DecomposeQuestionResponse
	decomposeErr      error
}

func (f *fakeCogitoService) SplitContent(ctx context.Context, req *cogitopb.SplitContentRequest) (*cogitopb.SplitContentResponse, error) {
	f.lastReq = req
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("authorization"); len(vals) > 0 {
			f.lastAuthMD = vals[0]
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

func (f *fakeCogitoService) DecomposeQuestion(_ context.Context, req *cogitopb.DecomposeQuestionRequest) (*cogitopb.DecomposeQuestionResponse, error) {
	f.lastDecomposeReq = req
	if f.decomposeErr != nil {
		return nil, f.decomposeErr
	}
	return f.decomposeResponse, nil
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
// don't need a live cogito. bearerToken is empty by default (existing tests'
// fake server doesn't check auth); use newTestClientWithToken for the
// auth-header test below.
func newTestClient(t *testing.T, dialer func(context.Context, string) (net.Conn, error)) *Client {
	t.Helper()
	return newTestClientWithToken(t, dialer, "")
}

func newTestClientWithToken(t *testing.T, dialer func(context.Context, string) (net.Conn, error), bearerToken string) *Client {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return &Client{conn: conn, cogito: cogitopb.NewCogitoServiceClient(conn), bearerToken: bearerToken}
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

func TestSplitContent_AttachesBearerTokenAsAuthMetadata(t *testing.T) {
	svc := &fakeCogitoService{response: &cogitopb.SplitContentResponse{}}
	dialer := startFakeCogito(t, svc)
	c := newTestClientWithToken(t, dialer, "shared-secret")

	_, _, err := c.SplitContent(context.Background(), "text", "")
	require.NoError(t, err)
	assert.Equal(t, "Bearer shared-secret", svc.lastAuthMD)
}

func TestDecomposeQuestion_SubQuestionsMode_ReturnsSubQuestionsOnly(t *testing.T) {
	svc := &fakeCogitoService{decomposeResponse: &cogitopb.DecomposeQuestionResponse{
		SubQuestions: []string{"What is X?", "How does Y relate to X?"},
	}}
	dialer := startFakeCogito(t, svc)
	c := newTestClient(t, dialer)

	subQuestions, taskCandidates, err := c.DecomposeQuestion(context.Background(), "What is X and how does Y relate?", DecomposeModeSubQuestions, nil)
	require.NoError(t, err)

	assert.Equal(t, []SubQuestion{{Text: "What is X?"}, {Text: "How does Y relate to X?"}}, subQuestions)
	assert.Empty(t, taskCandidates)

	require.NotNil(t, svc.lastDecomposeReq)
	assert.Equal(t, "What is X and how does Y relate?", svc.lastDecomposeReq.Message)
	assert.Equal(t, cogitopb.DecomposeMode_SUB_QUESTIONS, svc.lastDecomposeReq.Mode)
	assert.Empty(t, svc.lastDecomposeReq.History)
}

func TestDecomposeQuestion_TaskCandidatesMode_ReturnsTaskCandidates(t *testing.T) {
	svc := &fakeCogitoService{decomposeResponse: &cogitopb.DecomposeQuestionResponse{
		SubQuestions: []string{"What is X?"},
		TaskCandidates: []*cogitopb.TaskCandidateProto{
			{Title: "Investigate X", Description: "Look into what X is"},
			{Title: "Document Y", Description: "Write up how Y relates to X"},
		},
	}}
	dialer := startFakeCogito(t, svc)
	c := newTestClient(t, dialer)

	history := []ChatTurn{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello, how can I help?"},
	}
	subQuestions, taskCandidates, err := c.DecomposeQuestion(context.Background(), "What is X?", DecomposeModeTaskCandidates, history)
	require.NoError(t, err)

	assert.Equal(t, []SubQuestion{{Text: "What is X?"}}, subQuestions)
	assert.Equal(t, []TaskCandidate{
		{Title: "Investigate X", Description: "Look into what X is"},
		{Title: "Document Y", Description: "Write up how Y relates to X"},
	}, taskCandidates)

	require.NotNil(t, svc.lastDecomposeReq)
	assert.Equal(t, cogitopb.DecomposeMode_TASK_CANDIDATES, svc.lastDecomposeReq.Mode)
	require.Len(t, svc.lastDecomposeReq.History, 2)
	assert.Equal(t, "user", svc.lastDecomposeReq.History[0].Role)
	assert.Equal(t, "hi", svc.lastDecomposeReq.History[0].Content)
	assert.Equal(t, "assistant", svc.lastDecomposeReq.History[1].Role)
	assert.Equal(t, "hello, how can I help?", svc.lastDecomposeReq.History[1].Content)
}

func TestDecomposeQuestion_CogitoError_Propagates(t *testing.T) {
	svc := &fakeCogitoService{decomposeErr: assertErr{}}
	dialer := startFakeCogito(t, svc)
	c := newTestClient(t, dialer)

	_, _, err := c.DecomposeQuestion(context.Background(), "text", DecomposeModeSubQuestions, nil)
	require.Error(t, err)
}

type assertErr struct{}

func (assertErr) Error() string { return "cogito unavailable" }

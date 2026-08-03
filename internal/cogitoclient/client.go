package cogitoclient

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/tojiuni/goquest/internal/cogitopb"
)

// Section is a locally-shaped, recursive mirror of cogitopb.Section — split
// content's hierarchical (title, contents, sub_sections) result.
type Section struct {
	Title       string
	Contents    string
	SubSections []Section
}

// Client wraps cogito's CogitoService for content-splitting calls.
type Client struct {
	conn        *grpc.ClientConn
	cogito      cogitopb.CogitoServiceClient
	bearerToken string
}

// New dials cogito's gRPC endpoint (plaintext — in-cluster, same trust model
// as brainclient.New). bearerToken is the shared secret cogito's
// bearer_auth_interceptor checks on every RPC (cogito
// src/cogito/server/interceptors.py — a single fixed value ALL callers must
// present, not per-caller identity); every call below attaches it as
// "authorization: Bearer <token>" metadata, or every RPC fails with
// UNAUTHENTICATED "invalid bearer token".
func New(addr, bearerToken string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("cogitoclient: dial %s: %w", addr, err)
	}
	return &Client{conn: conn, cogito: cogitopb.NewCogitoServiceClient(conn), bearerToken: bearerToken}, nil
}

// Close closes the underlying gRPC connection.
func (c *Client) Close() error { return c.conn.Close() }

// withAuth attaches the shared bearer token as outgoing gRPC metadata.
func (c *Client) withAuth(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.bearerToken)
}

// SplitContent splits text into a table of contents and hierarchical
// sections via cogito's SplitContent RPC. title is optional prompt context.
func (c *Client) SplitContent(ctx context.Context, text, title string) ([]Section, []string, error) {
	resp, err := c.cogito.SplitContent(c.withAuth(ctx), &cogitopb.SplitContentRequest{
		Text:  text,
		Title: title,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("cogitoclient: SplitContent: %w", err)
	}

	return toSections(resp.Sections), resp.Toc, nil
}

// ChatTurn is a locally-shaped mirror of cogitopb.ChatTurn — one turn of
// prior conversation history passed as context to DecomposeQuestion.
type ChatTurn struct {
	Role    string
	Content string
}

// SubQuestion is a locally-shaped mirror of one entry in
// cogitopb.DecomposeQuestionResponse.sub_questions.
type SubQuestion struct {
	Text string
}

// TaskCandidate is a locally-shaped mirror of cogitopb.TaskCandidateProto —
// a goquest ticket candidate (title, description) suggested by cogito.
type TaskCandidate struct {
	Title       string
	Description string
}

// DecomposeMode selects whether DecomposeQuestion returns plain sub-questions
// or sub-questions plus goquest ticket candidates.
type DecomposeMode int

const (
	// DecomposeModeSubQuestions maps to cogitopb.DecomposeMode_SUB_QUESTIONS.
	DecomposeModeSubQuestions DecomposeMode = iota
	// DecomposeModeTaskCandidates maps to cogitopb.DecomposeMode_TASK_CANDIDATES.
	DecomposeModeTaskCandidates
)

// toProto maps a local DecomposeMode to its cogitopb.DecomposeMode wire value.
func (m DecomposeMode) toProto() cogitopb.DecomposeMode {
	if m == DecomposeModeTaskCandidates {
		return cogitopb.DecomposeMode_TASK_CANDIDATES
	}
	return cogitopb.DecomposeMode_SUB_QUESTIONS
}

// DecomposeQuestion splits a question into sub-questions via cogito's
// DecomposeQuestion RPC, optionally also returning goquest ticket candidates
// when mode is DecomposeModeTaskCandidates (task_candidates is empty for
// DecomposeModeSubQuestions). history carries prior chat turns for context —
// nil/empty is fine for a single-turn call.
func (c *Client) DecomposeQuestion(ctx context.Context, message string, mode DecomposeMode, history []ChatTurn) ([]SubQuestion, []TaskCandidate, error) {
	resp, err := c.cogito.DecomposeQuestion(c.withAuth(ctx), &cogitopb.DecomposeQuestionRequest{
		Message: message,
		History: toPBChatTurns(history),
		Mode:    mode.toProto(),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("cogitoclient: DecomposeQuestion: %w", err)
	}

	return toSubQuestions(resp.SubQuestions), toTaskCandidates(resp.TaskCandidates), nil
}

// toPBChatTurns maps the local ChatTurn type to cogitopb's ChatTurn message.
func toPBChatTurns(history []ChatTurn) []*cogitopb.ChatTurn {
	if len(history) == 0 {
		return nil
	}
	turns := make([]*cogitopb.ChatTurn, 0, len(history))
	for _, h := range history {
		turns = append(turns, &cogitopb.ChatTurn{Role: h.Role, Content: h.Content})
	}
	return turns
}

// toSubQuestions maps cogitopb's sub_questions strings to the local
// SubQuestion type.
func toSubQuestions(subQuestions []string) []SubQuestion {
	if len(subQuestions) == 0 {
		return nil
	}
	out := make([]SubQuestion, 0, len(subQuestions))
	for _, q := range subQuestions {
		out = append(out, SubQuestion{Text: q})
	}
	return out
}

// toTaskCandidates maps cogitopb's TaskCandidateProto message to the local
// TaskCandidate type.
func toTaskCandidates(pbCandidates []*cogitopb.TaskCandidateProto) []TaskCandidate {
	if len(pbCandidates) == 0 {
		return nil
	}
	out := make([]TaskCandidate, 0, len(pbCandidates))
	for _, c := range pbCandidates {
		out = append(out, TaskCandidate{Title: c.Title, Description: c.Description})
	}
	return out
}

// toSections maps cogitopb's recursive Section message to the local Section
// type, preserving nesting.
func toSections(pbSections []*cogitopb.Section) []Section {
	if len(pbSections) == 0 {
		return nil
	}
	sections := make([]Section, 0, len(pbSections))
	for _, s := range pbSections {
		sections = append(sections, Section{
			Title:       s.Title,
			Contents:    s.Contents,
			SubSections: toSections(s.SubSections),
		})
	}
	return sections
}

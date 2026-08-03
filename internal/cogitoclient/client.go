package cogitoclient

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

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
	conn   *grpc.ClientConn
	cogito cogitopb.CogitoServiceClient
}

// New dials cogito's gRPC endpoint (plaintext — in-cluster, same trust model
// as brainclient.New).
func New(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("cogitoclient: dial %s: %w", addr, err)
	}
	return &Client{conn: conn, cogito: cogitopb.NewCogitoServiceClient(conn)}, nil
}

// Close closes the underlying gRPC connection.
func (c *Client) Close() error { return c.conn.Close() }

// SplitContent splits text into a table of contents and hierarchical
// sections via cogito's SplitContent RPC. title is optional prompt context.
func (c *Client) SplitContent(ctx context.Context, text, title string) ([]Section, []string, error) {
	resp, err := c.cogito.SplitContent(ctx, &cogitopb.SplitContentRequest{
		Text:  text,
		Title: title,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("cogitoclient: SplitContent: %w", err)
	}

	return toSections(resp.Sections), resp.Toc, nil
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

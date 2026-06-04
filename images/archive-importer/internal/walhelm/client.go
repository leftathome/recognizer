// Package walhelmsrc defines the recognizer's own seam over the upstream
// walhelm-go client. The interface takes a plain since time.Time so downstream
// SP3 tasks (session/tree/state/fetch) can depend on a narrow, fakeable surface
// rather than walhelm-go's variadic ListOption API.
package walhelmsrc

import (
	"context"
	"time"

	walhelm "github.com/leftathome/walhelm-go"
)

// WalhelmClient is the recognizer-owned seam over walhelm-go. It exposes the
// operations the archive importer needs, normalizing the upstream variadic
// option API to a simple since time.Time.
type WalhelmClient interface {
	GetFolders(ctx context.Context) ([]walhelm.Folder, error)
	ListConversations(ctx context.Context, folderID string, since time.Time) ([]walhelm.ConversationSummary, error)
	GetConversation(ctx context.Context, id string) (*walhelm.Conversation, error)
	ListLabPanels(ctx context.Context, since time.Time) ([]walhelm.LabPanel, error)
	ListRecords(ctx context.Context, since time.Time) ([]walhelm.MedicalRecord, error)
	AcqAccountID() string
}

// realClient adapts a *walhelm.Client to the WalhelmClient seam.
type realClient struct {
	c *walhelm.Client
}

// newRealClient wraps an upstream walhelm-go client in the seam interface.
func newRealClient(c *walhelm.Client) WalhelmClient {
	return &realClient{c: c}
}

// sinceOpts builds the upstream ListOption slice, applying WithSince only when
// a non-zero time is supplied.
func sinceOpts(since time.Time) []walhelm.ListOption {
	opts := []walhelm.ListOption{}
	if !since.IsZero() {
		opts = append(opts, walhelm.WithSince(since))
	}
	return opts
}

func (r *realClient) GetFolders(ctx context.Context) ([]walhelm.Folder, error) {
	return r.c.GetFolders(ctx)
}

func (r *realClient) ListConversations(ctx context.Context, folderID string, since time.Time) ([]walhelm.ConversationSummary, error) {
	return r.c.ListConversations(ctx, folderID, sinceOpts(since)...)
}

func (r *realClient) GetConversation(ctx context.Context, id string) (*walhelm.Conversation, error) {
	return r.c.GetConversation(ctx, id)
}

func (r *realClient) ListLabPanels(ctx context.Context, since time.Time) ([]walhelm.LabPanel, error) {
	return r.c.ListLabPanels(ctx, sinceOpts(since)...)
}

func (r *realClient) ListRecords(ctx context.Context, since time.Time) ([]walhelm.MedicalRecord, error) {
	return r.c.ListRecords(ctx, sinceOpts(since)...)
}

func (r *realClient) AcqAccountID() string {
	sess := r.c.ExportSession()
	if sess == nil {
		return ""
	}
	return sess.UserID
}

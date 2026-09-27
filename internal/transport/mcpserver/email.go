package mcpserver

import (
	"context"
	"errors"

	"github.com/leofelipet/contexta/internal/email"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func openWorldReadTool(name, description string) *mcp.Tool {
	openWorld := true
	destructive := false
	return &mcp.Tool{
		Name: name, Description: description,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &openWorld, DestructiveHint: &destructive,
		},
	}
}

func openWorldWriteTool(name, description string) *mcp.Tool {
	openWorld := true
	destructive := false
	return &mcp.Tool{
		Name: name, Description: description,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: false, IdempotentHint: true, OpenWorldHint: &openWorld, DestructiveHint: &destructive,
		},
	}
}

func destructiveOpenWorldTool(name, description string) *mcp.Tool {
	openWorld := true
	destructive := true
	return &mcp.Tool{
		Name: name, Description: description,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: false, IdempotentHint: false, OpenWorldHint: &openWorld, DestructiveHint: &destructive,
		},
	}
}

func (s *server) addEmailTools(mcpServer *mcp.Server) {
	if s.email == nil {
		return
	}
	mcp.AddTool(mcpServer, readOnlyTool("list_email_accounts", "List configured corporate email accounts (no secrets). Prefer enabled accounts for mailbox operations."), s.listEmailAccounts)
	mcp.AddTool(mcpServer, openWorldReadTool("list_mailboxes", "List IMAP folders/mailboxes for one email account."), s.listMailboxes)
	mcp.AddTool(mcpServer, openWorldReadTool("search_emails", "Search emails live via IMAP (folder, unseen, from/to/subject, date range)."), s.searchEmails)
	mcp.AddTool(mcpServer, destructiveOpenWorldTool("send_email", "Send an email via the account SMTP settings and file a copy in Sent when the account has save_sent_copy. Destructive: delivers to external recipients."), s.sendEmail)
	mcp.AddTool(mcpServer, openWorldReadTool("get_email", "Fetch one email by IMAP UID including truncated body text/html and attachment metadata. Does not mark the email as read; call mark_email_read for that."), s.getEmail)
	mcp.AddTool(mcpServer, openWorldWriteTool("mark_email_read", "Mark one or more emails (up to 100 UIDs in the same folder) as read, or as unread with read=false."), s.markEmailRead)
	mcp.AddTool(mcpServer, openWorldWriteTool("set_email_flags", "Add or remove IMAP flags (e.g. seen, flagged) on one message."), s.setEmailFlags)
	mcp.AddTool(mcpServer, openWorldWriteTool("move_email", "Move one email to another mailbox (use for archive)."), s.moveEmail)
	mcp.AddTool(mcpServer, destructiveOpenWorldTool("delete_email", "Delete one email (prefer Trash SPECIAL-USE folder, otherwise flag+expunge)."), s.deleteEmail)
}

type listEmailAccountsInput struct {
	EnabledOnly bool   `json:"enabled_only,omitempty" jsonschema:"When true, only return enabled accounts."`
	Limit       int    `json:"limit,omitempty" jsonschema:"Maximum accounts to return, up to 100."`
	Cursor      string `json:"cursor,omitempty" jsonschema:"Opaque cursor from a previous list call."`
}

type listEmailAccountsOutput struct {
	Accounts   []email.Account `json:"accounts"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

func (s *server) listEmailAccounts(ctx context.Context, _ *mcp.CallToolRequest, input listEmailAccountsInput) (*mcp.CallToolResult, listEmailAccountsOutput, error) {
	s.logAccess(ctx, "list_email_accounts")
	page, err := s.email.ListAccounts(ctx, email.ListParams{
		EnabledOnly: input.EnabledOnly, Limit: mcpLimit(input.Limit), Cursor: input.Cursor,
	})
	if err != nil {
		s.logError(ctx, "list_email_accounts", err)
		return nil, listEmailAccountsOutput{}, safeEmailToolError(err)
	}
	return nil, listEmailAccountsOutput{Accounts: page.Accounts, NextCursor: page.NextCursor}, nil
}

type accountIDInput struct {
	AccountID string `json:"account_id" jsonschema:"Contexta email account ID."`
}

type listMailboxesOutput struct {
	Mailboxes []email.Mailbox `json:"mailboxes"`
}

func (s *server) listMailboxes(ctx context.Context, _ *mcp.CallToolRequest, input accountIDInput) (*mcp.CallToolResult, listMailboxesOutput, error) {
	s.logAccess(ctx, "list_mailboxes")
	list, err := s.email.ListMailboxes(ctx, input.AccountID)
	if err != nil {
		s.logError(ctx, "list_mailboxes", err)
		return nil, listMailboxesOutput{}, safeEmailToolError(err)
	}
	return nil, listMailboxesOutput{Mailboxes: list}, nil
}

type searchEmailsInput struct {
	AccountID string `json:"account_id" jsonschema:"Contexta email account ID."`
	Folder    string `json:"folder,omitempty" jsonschema:"IMAP mailbox name. Defaults to INBOX."`
	Unseen    bool   `json:"unseen,omitempty" jsonschema:"When true, only unseen messages."`
	From      string `json:"from,omitempty" jsonschema:"Match From header."`
	To        string `json:"to,omitempty" jsonschema:"Match To header."`
	Subject   string `json:"subject,omitempty" jsonschema:"Match Subject header."`
	Since     string `json:"since,omitempty" jsonschema:"Inclusive RFC3339 or YYYY-MM-DD."`
	Before    string `json:"before,omitempty" jsonschema:"Exclusive RFC3339 or inclusive YYYY-MM-DD end."`
	Limit     int    `json:"limit,omitempty" jsonschema:"Maximum messages, up to 50."`
}

type searchEmailsOutput struct {
	Emails []email.EmailSummary `json:"emails"`
}

func (s *server) searchEmails(ctx context.Context, _ *mcp.CallToolRequest, input searchEmailsInput) (*mcp.CallToolResult, searchEmailsOutput, error) {
	s.logAccess(ctx, "search_emails")
	since, before, err := parseRange(input.Since, input.Before)
	if err != nil {
		return nil, searchEmailsOutput{}, err
	}
	list, err := s.email.SearchEmails(ctx, input.AccountID, email.SearchParams{
		Folder: input.Folder, Unseen: input.Unseen, From: input.From, To: input.To,
		Subject: input.Subject, Since: since, Before: before, Limit: input.Limit,
	})
	if err != nil {
		s.logError(ctx, "search_emails", err)
		return nil, searchEmailsOutput{}, safeEmailToolError(err)
	}
	return nil, searchEmailsOutput{Emails: list}, nil
}

type getEmailInput struct {
	AccountID string `json:"account_id" jsonschema:"Contexta email account ID."`
	Folder    string `json:"folder,omitempty" jsonschema:"IMAP mailbox name. Defaults to INBOX."`
	UID       uint32 `json:"uid" jsonschema:"IMAP message UID."`
}

type getEmailOutput struct {
	Email email.EmailMessage `json:"email"`
}

func (s *server) getEmail(ctx context.Context, _ *mcp.CallToolRequest, input getEmailInput) (*mcp.CallToolResult, getEmailOutput, error) {
	s.logAccess(ctx, "get_email")
	msg, err := s.email.GetEmail(ctx, input.AccountID, input.Folder, input.UID)
	if err != nil {
		s.logError(ctx, "get_email", err)
		return nil, getEmailOutput{}, safeEmailToolError(err)
	}
	return nil, getEmailOutput{Email: msg}, nil
}

type sendEmailInput struct {
	AccountID  string   `json:"account_id" jsonschema:"Contexta email account ID."`
	To         []string `json:"to" jsonschema:"Recipient addresses."`
	CC         []string `json:"cc,omitempty" jsonschema:"CC recipients."`
	BCC        []string `json:"bcc,omitempty" jsonschema:"BCC recipients."`
	Subject    string   `json:"subject" jsonschema:"Email subject."`
	BodyText   string   `json:"body_text,omitempty" jsonschema:"Plain-text body."`
	BodyHTML   string   `json:"body_html,omitempty" jsonschema:"HTML body."`
	InReplyTo  string   `json:"in_reply_to,omitempty" jsonschema:"In-Reply-To message-id header."`
	References string   `json:"references,omitempty" jsonschema:"References header."`
}

type sendEmailOutput struct {
	Sent          bool   `json:"sent"`
	MessageID     string `json:"message_id,omitempty"`
	SavedToSent   bool   `json:"saved_to_sent"`
	SaveSentError string `json:"save_sent_error,omitempty"`
}

func (s *server) sendEmail(ctx context.Context, _ *mcp.CallToolRequest, input sendEmailInput) (*mcp.CallToolResult, sendEmailOutput, error) {
	s.logAccess(ctx, "send_email")
	result, err := s.email.SendEmail(ctx, input.AccountID, email.SendParams{
		To: input.To, CC: input.CC, BCC: input.BCC, Subject: input.Subject,
		BodyText: input.BodyText, BodyHTML: input.BodyHTML,
		InReplyTo: input.InReplyTo, References: input.References,
	})
	if err != nil {
		s.logError(ctx, "send_email", err)
		return nil, sendEmailOutput{}, safeEmailToolError(err)
	}
	return nil, sendEmailOutput{
		Sent: true, MessageID: result.MessageID, SavedToSent: result.SavedToSent, SaveSentError: result.SaveSentError,
	}, nil
}

type setEmailFlagsInput struct {
	AccountID string   `json:"account_id" jsonschema:"Contexta email account ID."`
	Folder    string   `json:"folder,omitempty" jsonschema:"IMAP mailbox name. Defaults to INBOX."`
	UID       uint32   `json:"uid" jsonschema:"IMAP message UID."`
	Add       []string `json:"add,omitempty" jsonschema:"Flags to add (seen, flagged, answered, draft, or \\Flag names)."`
	Remove    []string `json:"remove,omitempty" jsonschema:"Flags to remove."`
}

type setEmailFlagsOutput struct {
	Updated bool `json:"updated"`
}

func (s *server) setEmailFlags(ctx context.Context, _ *mcp.CallToolRequest, input setEmailFlagsInput) (*mcp.CallToolResult, setEmailFlagsOutput, error) {
	s.logAccess(ctx, "set_email_flags")
	err := s.email.SetEmailFlags(ctx, input.AccountID, input.Folder, []uint32{input.UID}, email.FlagUpdate{
		Add: input.Add, Remove: input.Remove,
	})
	if err != nil {
		s.logError(ctx, "set_email_flags", err)
		return nil, setEmailFlagsOutput{}, safeEmailToolError(err)
	}
	return nil, setEmailFlagsOutput{Updated: true}, nil
}

type markEmailReadInput struct {
	AccountID string   `json:"account_id" jsonschema:"Contexta email account ID."`
	Folder    string   `json:"folder,omitempty" jsonschema:"IMAP mailbox name. Defaults to INBOX."`
	UIDs      []uint32 `json:"uids" jsonschema:"One to 100 IMAP message UIDs from the same folder."`
	Read      *bool    `json:"read,omitempty" jsonschema:"true marks as read (default); false marks as unread."`
}

type markEmailReadOutput struct {
	Updated int  `json:"updated"`
	Read    bool `json:"read"`
}

func (s *server) markEmailRead(ctx context.Context, _ *mcp.CallToolRequest, input markEmailReadInput) (*mcp.CallToolResult, markEmailReadOutput, error) {
	s.logAccess(ctx, "mark_email_read")
	read := input.Read == nil || *input.Read
	if err := s.email.MarkRead(ctx, input.AccountID, input.Folder, input.UIDs, read); err != nil {
		s.logError(ctx, "mark_email_read", err)
		return nil, markEmailReadOutput{}, safeEmailToolError(err)
	}
	return nil, markEmailReadOutput{Updated: len(input.UIDs), Read: read}, nil
}

type moveEmailInput struct {
	AccountID string `json:"account_id" jsonschema:"Contexta email account ID."`
	Folder    string `json:"folder,omitempty" jsonschema:"Source IMAP mailbox. Defaults to INBOX."`
	UID       uint32 `json:"uid" jsonschema:"IMAP message UID."`
	Dest      string `json:"dest" jsonschema:"Destination mailbox (e.g. Archive)."`
}

type moveEmailOutput struct {
	Moved bool `json:"moved"`
}

func (s *server) moveEmail(ctx context.Context, _ *mcp.CallToolRequest, input moveEmailInput) (*mcp.CallToolResult, moveEmailOutput, error) {
	s.logAccess(ctx, "move_email")
	err := s.email.MoveEmail(ctx, input.AccountID, input.Folder, input.UID, input.Dest)
	if err != nil {
		s.logError(ctx, "move_email", err)
		return nil, moveEmailOutput{}, safeEmailToolError(err)
	}
	return nil, moveEmailOutput{Moved: true}, nil
}

type deleteEmailInput struct {
	AccountID string `json:"account_id" jsonschema:"Contexta email account ID."`
	Folder    string `json:"folder,omitempty" jsonschema:"IMAP mailbox name. Defaults to INBOX."`
	UID       uint32 `json:"uid" jsonschema:"IMAP message UID."`
}

type deleteEmailOutput struct {
	Deleted bool `json:"deleted"`
}

func (s *server) deleteEmail(ctx context.Context, _ *mcp.CallToolRequest, input deleteEmailInput) (*mcp.CallToolResult, deleteEmailOutput, error) {
	s.logAccess(ctx, "delete_email")
	err := s.email.DeleteEmail(ctx, input.AccountID, input.Folder, input.UID)
	if err != nil {
		s.logError(ctx, "delete_email", err)
		return nil, deleteEmailOutput{}, safeEmailToolError(err)
	}
	return nil, deleteEmailOutput{Deleted: true}, nil
}

func safeEmailToolError(err error) error {
	switch {
	case errors.Is(err, email.ErrNotFound):
		return errors.New("email account not found")
	case errors.Is(err, email.ErrMessageNotFound):
		return errors.New("email message not found")
	case errors.Is(err, email.ErrProvider):
		return err
	case errors.Is(err, email.ErrInvalidArgument):
		return errors.New("invalid argument")
	case errors.Is(err, email.ErrDisabled):
		return errors.New("email account is disabled")
	case errors.Is(err, email.ErrMissingKey):
		return errors.New("email credentials key is not configured")
	default:
		return safeToolError(err)
	}
}

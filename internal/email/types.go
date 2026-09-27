package email

import "time"

const (
	DefaultSearchLimit = 20
	MaxSearchLimit     = 50
	MaxBodyBytes       = 100 << 10 // 100 KiB truncated body for agents
)

type Account struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	Username string `json:"username"`
	IMAPHost string `json:"imap_host"`
	IMAPPort int    `json:"imap_port"`
	IMAPTLS  bool   `json:"imap_use_tls"`
	SMTPHost string `json:"smtp_host"`
	SMTPPort int    `json:"smtp_port"`
	SMTPTLS  bool   `json:"smtp_use_tls"`
	// SaveSentCopy appends sent mail to the Sent folder via IMAP. Disable for
	// providers that already do it on SMTP submission (Gmail, Microsoft 365).
	SaveSentCopy bool      `json:"save_sent_copy"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AccountSecrets holds plaintext credentials used only in-process (never JSON).
type AccountSecrets struct {
	Account
	Password string
}

type CreateParams struct {
	Name     string
	Address  string
	Username string
	Password string
	IMAPHost string
	IMAPPort int
	IMAPTLS  bool
	SMTPHost string
	SMTPPort int
	SMTPTLS  bool
	// SaveSentCopy nil means DefaultSaveSentCopy(IMAPHost).
	SaveSentCopy *bool
	Enabled      bool
}

type UpdateParams struct {
	Name         *string
	Address      *string
	Username     *string
	Password     *string
	IMAPHost     *string
	IMAPPort     *int
	IMAPTLS      *bool
	SMTPHost     *string
	SMTPPort     *int
	SMTPTLS      *bool
	SaveSentCopy *bool
	Enabled      *bool
}

type ListParams struct {
	EnabledOnly bool
	Limit       int
	Cursor      string
}

type Page struct {
	Accounts   []Account
	NextCursor string
}

type Mailbox struct {
	Name       string   `json:"name"`
	Delimiter  string   `json:"delimiter,omitempty"`
	Attributes []string `json:"attributes,omitempty"`
}

type Address struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

type AttachmentMeta struct {
	PartID   string `json:"part_id,omitempty"`
	Filename string `json:"filename,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
	Size     int    `json:"size,omitempty"`
}

type EmailSummary struct {
	UID     uint32    `json:"uid"`
	Folder  string    `json:"folder"`
	Subject string    `json:"subject,omitempty"`
	From    []Address `json:"from,omitempty"`
	To      []Address `json:"to,omitempty"`
	Date    time.Time `json:"date,omitempty"`
	Flags   []string  `json:"flags,omitempty"`
	Size    int       `json:"size,omitempty"`
}

type EmailMessage struct {
	EmailSummary
	CC            []Address        `json:"cc,omitempty"`
	BCC           []Address        `json:"bcc,omitempty"`
	ReplyTo       []Address        `json:"reply_to,omitempty"`
	MessageID     string           `json:"message_id,omitempty"`
	InReplyTo     string           `json:"in_reply_to,omitempty"`
	BodyText      string           `json:"body_text,omitempty"`
	BodyHTML      string           `json:"body_html,omitempty"`
	BodyTruncated bool             `json:"body_truncated,omitempty"`
	Attachments   []AttachmentMeta `json:"attachments,omitempty"`
}

type SearchParams struct {
	Folder  string
	Unseen  bool
	From    string
	To      string
	Subject string
	Since   *time.Time
	Before  *time.Time
	Limit   int
}

type SendParams struct {
	To         []string
	CC         []string
	BCC        []string
	Subject    string
	BodyText   string
	BodyHTML   string
	InReplyTo  string
	References string
}

type FlagUpdate struct {
	Add    []string
	Remove []string
}

type SendResult struct {
	MessageID     string `json:"message_id,omitempty"`
	SavedToSent   bool   `json:"saved_to_sent"`
	SaveSentError string `json:"save_sent_error,omitempty"`
}

type TestResult struct {
	IMAPOK    bool   `json:"imap_ok"`
	SMTPOK    bool   `json:"smtp_ok"`
	IMAPError string `json:"imap_error,omitempty"`
	SMTPError string `json:"smtp_error,omitempty"`
}

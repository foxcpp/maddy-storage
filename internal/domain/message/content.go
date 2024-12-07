package message

import (
	"strings"
	"time"

	gomail "github.com/emersion/go-message/mail"
)

type Disposition struct {
	Value  string            `json:"value"`
	Params map[string]string `json:"params,omitempty"`
}

type ContentData struct{}

type Address = gomail.Address

type ContentEnvelope struct {
	Date      time.Time  `json:"date"`
	Subject   string     `json:"subject"`
	From      []*Address `json:"from,omitempty"`
	Sender    []*Address `json:"sender,omitempty"`
	ReplyTo   []*Address `json:"reply_to,omitempty"`
	To        []*Address `json:"to,omitempty"`
	Cc        []*Address `json:"cc,omitempty"`
	Bcc       []*Address `json:"bcc,omitempty"`
	InReplyTo []string   `json:"in_reply_to,omitempty"`
	MessageID string     `json:"message_id"`
}

type ContentPartData struct {
	// Populated based on MIME header for the part or root message header.
	Type        string       `json:"type"`                  // Content-Type value (text/plain)
	Disposition *Disposition `json:"disposition,omitempty"` // Content-Disposition

	// Populated based on MIME header for the part or root message header.
	Params         map[string]string `json:"params,omitempty"`      // Content-Type params
	ID             string            `json:"id,omitempty"`          // Content-ID
	Description    string            `json:"description,omitempty"` // Content-Description
	Encoding       string            `json:"encoding,omitempty"`    // Content-Transfer-Encoding
	Language       []string          `json:"language,omitempty"`    // Content-Language tags
	Location       string            `json:"location,omitempty"`    // Content-Location link
	Size           uint32            `json:"size,omitempty"`        // Size of part body in octets.
	HeaderSize     uint32            `json:"header_size,omitempty"` // Size of part MIME header in octets (for Nested - of RFC822 header).
	HeaderNumLines int64             `json:"header_num_lines,omitempty"`
	NumLines       int64             `json:"num_lines,omitempty"` // Amount of LFs in body, populated for text/* only.

	Nested   *ContentPartData `json:"nested,omitempty"` // Populated only if RFC822 is stored inside MIME part.
	Envelope *ContentEnvelope `json:"envelope,omitempty"`
}

func (c *ContentPartData) IsMultipart() bool {
	return strings.HasPrefix(strings.ToLower(c.Type), "multipart/")
}

func (c *ContentPartData) HasNestedRFC822() bool {
	return strings.EqualFold(c.Type, "message/rfc822") ||
		strings.EqualFold(c.Type, "message/global")
}

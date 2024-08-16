package message

import "time"

type Disposition struct {
	Value  string            `json:"value"`
	Params map[string]string `json:"params,omitempty"`
}

type ContentData struct {
	Type        string            `json:"type"`             // multipart/mixed, may duplicate parts[0].type
	Params      map[string]string `json:"params,omitempty"` // contentType params
	Disposition Disposition       `json:"disposition,omitempty"`
	Language    []string          `json:"language,omitempty"`
	Location    string            `json:"location,omitempty"`
}

type Address struct {
	Name    string `json:"name"`
	Mailbox string `json:"mailbox"`
	Host    string `json:"host"`
}

type ContentEnvelope struct {
	Date      time.Time `json:"date"`
	Subject   string    `json:"subject"`
	From      []Address `json:"from,omitempty"`
	Sender    []Address `json:"sender,omitempty"`
	ReplyTo   []Address `json:"reply_to,omitempty"`
	To        []Address `json:"to,omitempty"`
	Cc        []Address `json:"cc,omitempty"`
	Bcc       []Address `json:"bcc,omitempty"`
	InReplyTo []string  `json:"in_reply_to,omitempty"`
	MessageID string    `json:"message_id"`
}

type ContentPartData struct {
	Type        string            `json:"type"`             // multipart/mixed, may duplicate parts[0].type
	Params      map[string]string `json:"params,omitempty"` // contentType params
	Disposition Disposition       `json:"disposition,omitempty"`
	Language    []string          `json:"language,omitempty"`
	Location    string            `json:"location,omitempty"`

	ID          string `json:"id,omitempty"`
	Description string `json:"description,omitempty"`
	Encoding    string `json:"encoding"`
	Size        uint32 `json:"size"`
	NumLines    int64  `json:"num_lines"`

	Envelope *ContentEnvelope `json:"envelope,omitempty"`
}

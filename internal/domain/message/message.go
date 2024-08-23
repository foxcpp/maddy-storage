package message

import (
	"fmt"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/metadata"
	"github.com/oklog/ulid/v2"
)

type Msg struct {
	ID_         ulid.ULID
	ReceivedAt_ time.Time
	CreatedAt_  time.Time
	UpdatedAt_  time.Time

	// Mutable fields.
	Meta_  metadata.Md
	Flags_ []string

	Content_ *ContentData
	Parts_   []Part
}

func (m *Msg) ID() ulid.ULID         { return m.ID_ }
func (m *Msg) ReceivedAt() time.Time { return m.ReceivedAt_ }
func (m *Msg) CreatedAt() time.Time  { return m.CreatedAt_ }
func (m *Msg) UpdatedAt() time.Time  { return m.UpdatedAt_ }
func (m *Msg) Meta() metadata.Md     { return m.Meta_ }
func (m *Msg) Flags() []string       { return m.Flags_ }
func (m *Msg) Content() *ContentData { return m.Content_ }
func (m *Msg) Parts() []Part         { return m.Parts_ }

func (m *Msg) Copy() *Msg {
	meta := m.Meta_.Copy()
	meta.Set("copy_of", m.ID_.String())

	return &Msg{
		ID_:         ulid.Make(),
		ReceivedAt_: m.ReceivedAt_,
		CreatedAt_:  time.Now(),
		UpdatedAt_:  time.Now(),
		Meta_:       meta,
		Parts_:      m.Parts_,
	}
}

type Part struct {
	// Immutable - no fields can be changed after creation.

	ID_   ulid.ULID
	Path_ Path

	Content_        *ContentPartData
	Inline_         []byte
	ExternalBlobID_ string
}

func (p *Part) ID() ulid.ULID             { return p.ID_ }
func (p *Part) Path() Path                { return p.Path_ }
func (p *Part) Content() *ContentPartData { return p.Content_ }
func (p *Part) InlineBlob() []byte        { return p.Inline_ }
func (p *Part) ExternalBlobID() string    { return p.ExternalBlobID_ }

type NewMsg struct {
	Date    time.Time // IMAP internal date, can be zero (will default to created_at)
	Flags   []string
	Content *ContentData
	Parts   []NewPart // must have at least one part (with path 1).
}

func (nm *NewMsg) Validate() error {
	for i, p := range nm.Parts {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("invalid part at index %d: %v", i, err)
		}
	}

	return nil
}

type NewPart struct {
	Path Path

	Content    *ContentPartData
	InlineBlob []byte
	ExternalID string
}

func (np *NewPart) Validate() error {
	if np.ExternalID == "" && np.InlineBlob == nil {
		return fmt.Errorf("no body content")
	}
	if np.Content == nil {
		return fmt.Errorf("no content data")
	}
	if np.InlineBlob != nil && uint32(len(np.InlineBlob)) != np.Content.Size {
		return fmt.Errorf("inline blob (%d octets) size is not equal to size (%d)", len(np.InlineBlob), np.Content.Size)
	}
	if np.Path.Empty() {
		return fmt.Errorf("empty part path")
	}

	return nil
}

func New(data *NewMsg) (*Msg, error) {
	// TODO: Populate metadata from context
	md := metadata.New()

	if err := data.Validate(); err != nil {
		return nil, fmt.Errorf("new msg: %v", err)
	}

	parts := make([]Part, len(data.Parts))
	for i, p := range data.Parts {
		parts[i] = Part{
			ID_:             ulid.Make(),
			Path_:           p.Path,
			Content_:        p.Content,
			Inline_:         p.InlineBlob,
			ExternalBlobID_: p.ExternalID,
		}
	}

	now := time.Now()
	msg := &Msg{
		ID_:         ulid.Make(),
		ReceivedAt_: data.Date,
		CreatedAt_:  now,
		UpdatedAt_:  now,
		Meta_:       md,
		Flags_:      data.Flags,
		Content_:    data.Content,
		Parts_:      parts,
	}
	if msg.ReceivedAt_.IsZero() {
		msg.ReceivedAt_ = msg.CreatedAt_
	}
	return msg, nil
}

package message

import (
	"fmt"
	"iter"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/metadata"
	"github.com/oklog/ulid/v2"
)

type Msg struct {
	ID              ulid.ULID
	ReceivedAt      time.Time
	CreatedAtModSeq folder.ModSeq
	ModSeq          folder.ModSeq
	CreatedAt       time.Time

	UpdatedAt time.Time
	// Mutable fields.
	Meta metadata.Md

	Flags     []string
	TotalSize uint32
	Content   *ContentData
	Parts     []Part
}

func (m *Msg) Copy(modSeq folder.ModSeq) *Msg {
	meta := m.Meta.Copy()
	meta.Set("copy_of", m.ID.String())

	parts := make([]Part, len(m.Parts))
	for i := range m.Parts {
		parts[i] = *m.Parts[i].Copy()
	}

	return &Msg{
		ID:              ulid.Make(),
		ReceivedAt:      m.ReceivedAt,
		CreatedAtModSeq: modSeq,
		ModSeq:          modSeq,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
		Meta:            meta,
		Flags:           m.Flags,
		TotalSize:       m.TotalSize,
		Content:         m.Content,
		Parts:           parts,
	}
}

func (m *Msg) calculateTotalSize() uint32 {
	total := uint32(0)
	for _, p := range m.Parts {
		total += p.TotalSize()
	}
	return total
}

func (m *Msg) FindPart(path Path) *Part {
	for _, p := range m.Parts {
		if p.Path.Equals(path) {
			return &p
		}
	}
	return nil
}

func (m *Msg) TextParts() iter.Seq[*Part] {
	return func(yield func(*Part) bool) {
		for _, p := range m.Parts {
			if p.IsText() && !yield(&p) {
				return
			}
		}
	}
}

// SearchableTextParts returns the parts should be considered
// for text-based search.
func (m *Msg) SearchableTextParts() iter.Seq[*Part] {
	// TODO: Exclude redundant multipart subparts for multipart/alternative.
	// e.g. search only text/plain while skipping text/html.
	return m.TextParts()
}

func (m *Msg) SearchParts() iter.Seq[*Part] {
	// We ignore non-text fields for any search as it may be too expensive
	// to try matching large binary blobs.
	return m.SearchableTextParts()
}

type Part struct {
	// Immutable - no fields can be changed after creation.

	ID    ulid.ULID
	Order int
	Path  Path

	Content        *ContentPartData
	Inline         []byte
	ExternalBlobID string
}

func (p *Part) Copy() *Part {
	return &Part{
		ID:             ulid.Make(),
		Order:          p.Order,
		Path:           p.Path,
		Content:        p.Content,
		Inline:         p.Inline,
		ExternalBlobID: p.ExternalBlobID,
	}
}

func (p *Part) TotalSize() uint32      { return p.Content.TotalSize() }
func (p *Part) TotalLines() int64      { return p.Content.TotalLines() }
func (p *Part) HasNestedMessage() bool { return p.Content.HasNestedMessage() }
func (p *Part) IsMessage() bool        { return p.Content.IsMessage() }
func (p *Part) IsMultipart() bool      { return p.Content.IsMultipart() }
func (p *Part) IsText() bool           { return p.Content.IsText() }
func (p *Part) IsMIMEPart() bool       { return p.Content.IsMIMEPart }

type NewMsg struct {
	ID      ulid.ULID
	ModSeq  folder.ModSeq
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
	if len(nm.Parts) == 0 {
		return fmt.Errorf("message should contain at least one part")
	}

	return nil
}

type NewPart struct {
	ID    ulid.ULID
	Order int
	Path  Path

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
	//if np.InlineBlob != nil && uint32(len(np.InlineBlob)) != np.Content.ContentSize+np.Content.HeaderSize {
	//	return fmt.Errorf("inline blob (%d octets) size is not equal to size (%d, %d)",
	//		len(np.InlineBlob), np.Content.ContentSize, np.Content.HeaderSize)
	//}

	return nil
}

func New(data *NewMsg, additionalMeta metadata.Md) (*Msg, error) {
	md := metadata.New()
	for k, v := range additionalMeta {
		md.Set(k, v)
	}

	if err := data.Validate(); err != nil {
		return nil, fmt.Errorf("new msg: %v", err)
	}

	parts := make([]Part, len(data.Parts))
	for i, p := range data.Parts {
		if p.ID == (ulid.ULID{}) {
			data.ID = ulid.Make()
		}
		parts[i] = Part{
			ID:             p.ID,
			Order:          p.Order,
			Path:           p.Path,
			Content:        p.Content,
			Inline:         p.InlineBlob,
			ExternalBlobID: p.ExternalID,
		}
	}

	if data.ID == (ulid.ULID{}) {
		data.ID = ulid.Make()
	}

	now := time.Now()
	msg := &Msg{
		ID:              data.ID,
		CreatedAtModSeq: data.ModSeq,
		ModSeq:          data.ModSeq,
		ReceivedAt:      data.Date,
		CreatedAt:       now,
		UpdatedAt:       now,
		Meta:            md,
		Flags:           data.Flags,
		Content:         data.Content,
		Parts:           parts,
	}
	msg.Content.Envelope = parts[0].Content.Envelope
	msg.TotalSize = msg.calculateTotalSize()
	if msg.ReceivedAt.IsZero() {
		msg.ReceivedAt = msg.CreatedAt
	}
	return msg, nil
}

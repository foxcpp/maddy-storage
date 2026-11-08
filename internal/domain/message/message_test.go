package message

import (
	"testing"

	"github.com/foxcpp/maddy-storage/internal/domain/metadata"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"
)

func TestNew_GeneratesMissingPartIDs(t *testing.T) {
	msgID := ulid.Make()
	keptPartID := ulid.Make()

	msg, err := New(&NewMsg{
		ID:      msgID,
		Content: &ContentData{},
		Parts: []NewPart{
			{Path: Path{}, Content: &ContentPartData{ContentSize: 1}, InlineBlob: []byte("a")},
			{ID: keptPartID, Path: Path{1}, Content: &ContentPartData{ContentSize: 2}, InlineBlob: []byte("b")},
			{Path: Path{2}, Content: &ContentPartData{ContentSize: 3}, InlineBlob: []byte("c")},
		},
	}, metadata.New())
	require.NoError(t, err)

	require.Equal(t, msgID, msg.ID, "message ID must not be replaced when a part has no ID")
	require.Equal(t, keptPartID, msg.Parts[1].ID)
	require.NotEqual(t, ulid.ULID{}, msg.Parts[0].ID)
	require.NotEqual(t, ulid.ULID{}, msg.Parts[2].ID)
	require.NotEqual(t, msg.Parts[0].ID, msg.Parts[2].ID)
}

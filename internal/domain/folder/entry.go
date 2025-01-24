package folder

import (
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

type Entry struct {
	FolderID        ulid.ULID
	MsgID           ulid.ULID
	IMAPUID         uint32
	ModSeq          ModSeq
	CreatedAtModSeq ModSeq
	SeqNum          uint32 // Read-only from DB, empty on creation
	CreatedAt       time.Time
	DeletedAt       time.Time // Returned only by watcher or DeletedEntries
}

func (e Entry) String() string {
	return fmt.Sprintf("{FolderID: %v, MsgID: %v, UID: %v}", e.FolderID, e.MsgID, e.IMAPUID)
}

func NewEntry(folderID, msgID ulid.ULID, uid uint32, modSeq ModSeq, createdAt time.Time) Entry {
	return Entry{
		FolderID:        folderID,
		MsgID:           msgID,
		IMAPUID:         uid,
		CreatedAtModSeq: modSeq,
		ModSeq:          modSeq,
		CreatedAt:       createdAt,
	}
}

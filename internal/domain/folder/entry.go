package folder

import "github.com/oklog/ulid/v2"

type Entry struct {
	FolderID_ ulid.ULID
	MsgID_    ulid.ULID
	UID_      uint32
}

func (e *Entry) FolderID() ulid.ULID { return e.FolderID_ }
func (e *Entry) MsgID() ulid.ULID    { return e.MsgID_ }
func (e *Entry) UID() uint32         { return e.UID_ }

func NewEntry(folderID, msgID ulid.ULID, uid uint32) Entry {
	return Entry{
		FolderID_: folderID,
		MsgID_:    msgID,
		UID_:      uid,
	}
}

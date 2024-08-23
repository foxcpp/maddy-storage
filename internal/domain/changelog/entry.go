package changelog

import (
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/metadata"
	"github.com/oklog/ulid/v2"
)

type Type string

const (
	TypeAccountCreated = "account.created"
	TypeAccountDeleted = "account.deleted"
	TypeFolderCreated  = "folder.created"
	TypeFolderRenamed  = "folder.renamed"
	TypeFolderDeleted  = "folder.deleted"
	TypeMessageCreated = "message.created"
	TypeMessageUpdated = "message.updated"
	TypeMessageDeleted = "message.deleted"
)

type AccountEntry struct {
}

type FolderEntry struct {
	OldName string
	NewName string
}

type MessageEntry struct {
	UID   uint32   // IMAP UID
	Flags []string // new flags, if changed
}

type Entry struct {
	At   time.Time
	Type Type

	AccountID ulid.ULID // populated for all entries
	FolderID  ulid.ULID // populated for all entries affecting folders and messages
	MessageID ulid.ULID // populated for all entries affecting messages

	Metadata metadata.Md
	Account  *AccountEntry
	Folder   *FolderEntry
	Message  *MessageEntry
}

func (e *Entry) ModSeq() int64 {
	return e.At.UnixMicro()
}

func NewMessage(type_ string, accountID, folderID, messageID ulid.ULID, msg *MessageEntry) *Entry {
	return &Entry{
		At:        time.Now(),
		Type:      Type(type_),
		AccountID: accountID,
		FolderID:  folderID,
		MessageID: messageID,
		Metadata:  metadata.New(),
		Message:   msg,
	}
}

func NewFolder(type_ string, accountID, folderID ulid.ULID, folder *FolderEntry) *Entry {
	return &Entry{
		At:        time.Now(),
		Type:      Type(type_),
		AccountID: accountID,
		FolderID:  folderID,
		Metadata:  metadata.New(),
		Folder:    folder,
	}
}

func NewAccount(type_ string, accountID ulid.ULID, acct *AccountEntry) *Entry {
	return &Entry{
		At:        time.Now(),
		Type:      Type(type_),
		AccountID: accountID,
		Account:   acct,
	}
}

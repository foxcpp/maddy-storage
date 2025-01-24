package utils

import (
	"sync"

	"github.com/emersion/go-imap/v2/imapclient"
)

type MetadataEntry struct {
	Mailbox string
	Entries []string
}

type UnilateralRecorder struct {
	lock sync.Mutex

	expunges  []uint32
	mailboxes []*imapclient.UnilateralDataMailbox
	fetch     []*imapclient.FetchMessageData
	metadata  []MetadataEntry
}

func NewUnilateralRecorder() *UnilateralRecorder {
	return &UnilateralRecorder{
		expunges:  []uint32{},
		mailboxes: []*imapclient.UnilateralDataMailbox{},
		fetch:     []*imapclient.FetchMessageData{},
	}
}

func (r *UnilateralRecorder) Handler() *imapclient.UnilateralDataHandler {
	if r == nil {
		return nil
	}
	return &imapclient.UnilateralDataHandler{
		Expunge:  r.Expunge,
		Mailbox:  r.Mailbox,
		Fetch:    r.Fetch,
		Metadata: r.Metadata,
	}
}

func (r *UnilateralRecorder) Expunge(seqNum uint32) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.expunges = append(r.expunges, seqNum)
}

func (r *UnilateralRecorder) PopExpunge() []uint32 {
	r.lock.Lock()
	defer r.lock.Unlock()

	expunges := r.expunges
	r.expunges = make([]uint32, 0)
	return expunges
}

func (r *UnilateralRecorder) Mailbox(data *imapclient.UnilateralDataMailbox) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.mailboxes = append(r.mailboxes, data)
}

func (r *UnilateralRecorder) PopMailbox() []*imapclient.UnilateralDataMailbox {
	r.lock.Lock()
	defer r.lock.Unlock()

	mailboxes := r.mailboxes
	r.mailboxes = make([]*imapclient.UnilateralDataMailbox, 0)
	return mailboxes
}

func (r *UnilateralRecorder) Fetch(msg *imapclient.FetchMessageData) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.fetch = append(r.fetch, msg)
}

func (r *UnilateralRecorder) PopFetch() []*imapclient.FetchMessageData {
	r.lock.Lock()
	defer r.lock.Unlock()

	fetch := r.fetch
	r.fetch = make([]*imapclient.FetchMessageData, 0)
	return fetch
}

func (r *UnilateralRecorder) Metadata(mailbox string, entries []string) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.metadata = append(r.metadata, MetadataEntry{Mailbox: mailbox, Entries: entries})
}

func (r *UnilateralRecorder) PopMetadata() []MetadataEntry {
	r.lock.Lock()
	defer r.lock.Unlock()

	metadata := r.metadata
	r.metadata = make([]MetadataEntry, 0)
	return metadata
}

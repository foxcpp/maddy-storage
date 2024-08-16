package imap2

import (
	"runtime/trace"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

func (s *session) Select(mailbox string, options *imap.SelectOptions) (*imap.SelectData, error) {
	//ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Select")
	//defer task.End()

	//TODO implement me
	panic("implement me")
}

func (s *session) Status(mailbox string, options *imap.StatusOptions) (*imap.StatusData, error) {
	//ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Status")
	//defer task.End()

	//TODO implement me
	panic("implement me")
}

func (s *session) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	//ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Expunge")
	//defer task.End()

	//TODO implement me
	panic("implement me")
}

func (s *session) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	//ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Search")
	//defer task.End()

	//TODO implement me
	panic("implement me")
}

func (s *session) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	//ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Fetch")
	//defer task.End()

	//TODO implement me
	panic("implement me")
}

func (s *session) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	//ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Store")
	//defer task.End()

	//TODO implement me
	panic("implement me")
}

func (s *session) Move(w *imapserver.MoveWriter, numSet imap.NumSet, dest string) error {
	//ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Move")
	//defer task.End()

	//TODO implement me
	panic("implement me")
}

func (s *session) Copy(numSet imap.NumSet, dest string) (*imap.CopyData, error) {
	//ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Copy")
	//defer task.End()

	//TODO implement me
	panic("implement me")
}

func (s *session) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	//ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Append")
	//defer task.End()

	//TODO implement me
	panic("implement me")
}

func (s *session) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error {
	_, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Poll")
	defer task.End()

	if s.updateHandler == nil {
		return nil
	}

	return s.updateHandler.Sync(w, allowExpunge)
}

func (s *session) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	_, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Idle")
	defer task.End()

	if s.updateHandler == nil {
		<-stop
		return nil
	}

	return s.updateHandler.Idle(w, stop)
}

package imap2

import (
	"runtime/trace"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"go.uber.org/zap"
)

func uidSetAsRange(set imap.UIDSet) []folder.UIDRange {
	res := make([]folder.UIDRange, 0, len(set))
	for _, ent := range set {
		res = append(res, folder.UIDRange{
			Since: uint32(ent.Start),
			Until: uint32(ent.Stop),
		})
	}
	return res
}

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
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Move")
	defer task.End()

	var uids imap.UIDSet
	var err error
	switch set := numSet.(type) {
	case *imap.UIDSet:
		uids, err = s.updateHandler.ResolveUID(*set)
	case *imap.SeqSet:
		uids, err = s.updateHandler.ResolveSeq(*set)
	default:
		panic("unexpected NumSet type")
	}
	if err != nil {
		return err
	}

	result, err := s.b.messages.MoveByUID(ctx, s.accountID, uidSetAsRange(uids), s.selectedFolderID, dest)
	if err != nil {
		return err
	}

	sourceUIDs := imap.UIDSet{}
	for _, ent := range result.SourceEntries {
		sourceUIDs.AddNum(imap.UID(ent.UID_))
	}
	targetUIDs := imap.UIDSet{}
	for _, ent := range result.TargetEntries {
		targetUIDs.AddNum(imap.UID(ent.UID_))
	}

	err = w.WriteCopyData(&imap.CopyData{
		UIDValidity: result.Target.UIDValidity_,
		SourceUIDs:  sourceUIDs,
		DestUIDs:    targetUIDs,
	})
	if err != nil {
		return err
	}
	storeRecent := s.b.updateManager.NewMessages(result.Target.ID_, targetUIDs)
	if storeRecent {
		// TODO: proper \Recent support
	}
	s.updateHandler.RemovedSet(sourceUIDs, true)

	if err := s.updateHandler.SyncSingleExpunge(w, sourceUIDs); err != nil {
		s.log.Error("update synchronization error", zap.Error(err))
		return s.c.Bye("Update synchronization failed, terminating connection to prevent corruption")
	}
	return nil
}

func (s *session) Copy(numSet imap.NumSet, dest string) (*imap.CopyData, error) {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Move")
	defer task.End()

	var uids imap.UIDSet
	var err error
	switch set := numSet.(type) {
	case *imap.UIDSet:
		uids, err = s.updateHandler.ResolveUID(*set)
	case *imap.SeqSet:
		uids, err = s.updateHandler.ResolveSeq(*set)
	default:
		panic("unexpected NumSet type")
	}
	if err != nil {
		return nil, err
	}

	result, err := s.b.messages.CopyByUID(ctx, s.accountID, uidSetAsRange(uids), s.selectedFolderID, dest)
	if err != nil {
		return nil, err
	}

	sourceUIDs := imap.UIDSet{}
	for _, ent := range result.SourceEntries {
		sourceUIDs.AddNum(imap.UID(ent.UID_))
	}
	targetUIDs := imap.UIDSet{}
	for _, ent := range result.TargetEntries {
		targetUIDs.AddNum(imap.UID(ent.UID_))
	}

	storeRecent := s.b.updateManager.NewMessages(result.Target.ID_, targetUIDs)
	if storeRecent {
		// TODO: proper \Recent support
	}

	return &imap.CopyData{
		UIDValidity: result.Target.UIDValidity_,
		SourceUIDs:  sourceUIDs,
		DestUIDs:    targetUIDs,
	}, nil
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

	err := s.updateHandler.Idle(w, stop)
	if err != nil {
		s.log.Error("update synchronization error in idle", zap.Error(err))
		return s.c.Bye("Update synchronization failed, terminating connection to prevent corruption")
	}
	return nil
}

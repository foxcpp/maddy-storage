package scan

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime/trace"
	"slices"

	"github.com/foxcpp/maddy-storage/internal/domain/blob"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
)

type Cfg struct {
	BatchSize int
}

type Searcher struct {
	cfg Cfg

	metaSearch searcher.Searcher
	msgRepo    message.Repo
	blobStore  blob.Store
}

func New(cfg Cfg, metaSearch searcher.Searcher, msgRepo message.Repo, bodyStore blob.Store) *Searcher {
	return &Searcher{
		cfg:        cfg,
		metaSearch: metaSearch,
		msgRepo:    msgRepo,
		blobStore:  bodyStore,
	}
}

type readCloser struct {
	io.Reader
	io.Closer
}

func (s *Searcher) matchMessage(ctx context.Context, ent *searcher.FoundMsg, msg *message.Msg, bodyCond *searcher.Cond) (bool, error) {
	return searcher.Match(ctx, ent, msg, func(ctx context.Context, part *message.Part) (io.ReadCloser, error) {
		var r io.Reader
		r = bytes.NewReader(part.Inline)

		if part.ExternalBlobID == "" {
			return io.NopCloser(r), nil
		}

		blobR, err := s.blobStore.Open(ctx, part.ExternalBlobID)
		if err != nil {
			return nil, storeerrors.InternalError{Reason: fmt.Errorf("writePart: failed to open blob: %w", err)}
		}
		r = io.MultiReader(r, blobR)

		return readCloser{
			Reader: r,
			Closer: blobR,
		}, nil
	}, bodyCond)
}

func (s *Searcher) matchBodyAndAggregate(
	ctx context.Context, bodyCond *searcher.Cond,
	opts searcher.Opts, metaResults searcher.SearchResult,
) (searcher.SearchResult, error) {
	res := searcher.SearchResult{}

	for foundMsgs := range slices.Chunk(metaResults.All, s.cfg.BatchSize) {
		msgIDs := make([]ulid.ULID, len(foundMsgs))
		entByID := make(map[ulid.ULID]*searcher.FoundMsg)
		for i, msg := range foundMsgs {
			msgIDs[i] = msg.MessageID
			entByID[msg.MessageID] = &msg
		}
		msgs, err := s.msgRepo.GetByIDs(ctx, 0, msgIDs...)
		if err != nil {
			contextlib.FromContext(ctx).Error("failed to fetch message batch, skipping", zap.Error(err))
			continue
		}

		for _, msg := range msgs {
			ent, ok := entByID[msg.ID]
			if !ok {
				continue
			}
			matched, err := s.matchMessage(ctx, ent, &msg, bodyCond)
			if err != nil {
				contextlib.FromContext(ctx).Error("failed to check message body match, skipping",
					zap.Error(err), zap.Stringer("msg_id", msg.ID))
				continue
			}
			if !matched {
				continue
			}

			res.Add(&opts, *ent)
		}
	}

	return res, nil
}

func (s *Searcher) Search(ctx context.Context, accountID ulid.ULID, cond *searcher.Cond, opts searcher.Opts) (searcher.SearchResult, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.searcher.scan.Search").End()

	if !cond.NeedsBody() {
		res, err := s.metaSearch.Search(ctx, accountID, cond, opts)
		if err != nil {
			return searcher.SearchResult{}, fmt.Errorf("failed to run metadata search: %w", err)
		}
		return res, nil
	}

	split := cond.SplitMetaBody()

	res, err := s.metaSearch.Search(ctx, accountID, split.Metadata, searcher.Opts{
		At:            opts.At,
		DeletesAt:     opts.DeletesAt,
		ReturnAll:     true,
		ReturnSeqNums: opts.ReturnSeqNums,
	})
	if err != nil {
		return searcher.SearchResult{}, fmt.Errorf("failed to run metadata search: %w", err)
	}

	return s.matchBodyAndAggregate(ctx, split.Body, opts, res)
}

func (s *Searcher) SearchFlag(ctx context.Context, accountID, folderID ulid.ULID, flag string, opts searcher.Opts) (searcher.SearchResult, error) {
	return s.Search(ctx, accountID, &searcher.Cond{
		FolderIDs: []ulid.ULID{folderID},
		Flag:      []string{flag},
	}, opts)
}

func (s *Searcher) SearchWithoutFlag(ctx context.Context, accountID, folderID ulid.ULID, flag string, opts searcher.Opts) (searcher.SearchResult, error) {
	return s.Search(ctx, accountID, &searcher.Cond{
		FolderIDs: []ulid.ULID{folderID},
		NoFlag:    []string{flag},
	}, opts)
}

func (s *Searcher) SearchFolder(ctx context.Context, accountID, folderID ulid.ULID, opts searcher.Opts) (searcher.SearchResult, error) {
	return s.Search(ctx, accountID, &searcher.Cond{
		FolderIDs: []ulid.ULID{folderID},
	}, opts)
}

func (s *Searcher) Tx(ctx context.Context, inTx func(ctx context.Context, s searcher.Searcher) error) error {
	return s.metaSearch.Tx(ctx, func(ctx context.Context, tx searcher.Searcher) error {
		return inTx(ctx, &Searcher{
			cfg:        s.cfg,
			metaSearch: tx,
			msgRepo:    s.msgRepo,
			blobStore:  s.blobStore,
		})
	})
}

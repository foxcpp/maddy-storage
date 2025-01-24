package messageusecase

import (
	"context"
	"fmt"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	"github.com/oklog/ulid/v2"
)

type InfoOpts struct {
	At folder.ModSeq

	ReturnNamespace bool
	ReturnIMAPMeta  bool
	ReturnMaxUID    bool
	CountMsgs       bool
	CountDeleted    bool
	CountUnseen     bool
	CountSize       bool
	UsedFlags       bool
	ReturnModSeq    bool
}

type FolderInfo struct {
	Folder    *folder.Folder
	Namespace *folder.Namespace
	IMAP      *folder.IMAPFolder
	UsedFlags []string

	MaxUID      uint32
	Msgs        uint32
	Size        int64
	DeletedMsgs uint32
	DeletedSize int64
	UnseenMsgs  uint32
	MaxModSeq   folder.ModSeq
	AcctModSeq  folder.ModSeq
}

func (uc *Usecase) fetchFolderInfo(ctx context.Context, f *folder.Folder, opts InfoOpts) (FolderInfo, error) {
	info := FolderInfo{
		Folder: f,
	}

	if opts.At == 0 {
		modSeq, err := uc.imapRepo.LastModSeq(ctx, f.AccountID)
		if err != nil {
			return FolderInfo{}, fmt.Errorf("last modseq: %w", err)
		}
		opts.At = modSeq
		info.AcctModSeq = modSeq
	}

	if opts.ReturnIMAPMeta {
		imap, err := uc.imapRepo.GetIMAPFolder(ctx, f.ID)
		if err != nil {
			return FolderInfo{}, fmt.Errorf("get imap folder: %w", err)
		}
		info.IMAP = &imap
	}

	if opts.UsedFlags {
		flags, err := uc.folderRepo.UsedFlags(ctx, f.ID)
		if err != nil {
			return FolderInfo{}, fmt.Errorf("failed to get used flags (%v): %w",
				f.ID, err)
		}
		info.UsedFlags = flags
	}

	var folderStats, unseenStats, deletedStats searcher.SearchResult
	if opts.CountMsgs || opts.CountDeleted || opts.CountUnseen || opts.CountSize || opts.ReturnMaxUID {
		var err error
		folderStats, err = uc.searcher.SearchFolder(
			ctx, f.AccountID, f.ID,
			searcher.Opts{
				At:              opts.At,
				DeletesAt:       opts.At,
				ReturnMaxUID:    opts.ReturnMaxUID,
				ReturnCount:     opts.CountMsgs,
				ReturnTotalSize: opts.CountSize,
			})
		if err != nil {
			return FolderInfo{}, fmt.Errorf("failed to fetch folder stats: %w", err)
		}

		if opts.CountUnseen {
			unseenStats, err = uc.searcher.SearchWithoutFlag(
				ctx, f.AccountID,
				f.ID, "\\Seen", searcher.Opts{
					At:          opts.At,
					DeletesAt:   opts.At,
					ReturnCount: true,
				})
			if err != nil {
				return FolderInfo{}, fmt.Errorf("failed to fetch unseen stats: %w", err)
			}
		}
		if opts.CountDeleted {
			deletedStats, err = uc.searcher.SearchFlag(
				ctx, f.AccountID,
				f.ID, "\\Deleted", searcher.Opts{
					At:          opts.At,
					DeletesAt:   opts.At,
					ReturnCount: true,
				})
			if err != nil {
				return FolderInfo{}, fmt.Errorf("failed to fetch deleted stats: %w", err)
			}
		}
	}

	if opts.CountMsgs {
		info.Msgs = folderStats.Count
	}
	if opts.CountDeleted {
		info.DeletedMsgs = deletedStats.Count
		info.DeletedSize = deletedStats.TotalSize
	}
	if opts.CountUnseen {
		info.UnseenMsgs = unseenStats.Count
	}
	if opts.CountSize {
		info.Size = folderStats.TotalSize
	}
	if opts.ReturnMaxUID {
		info.MaxUID = folderStats.MaxUID
	}

	return info, nil
}

func (uc *Usecase) FetchFolderInfoByID(
	ctx context.Context, accountID, folderID ulid.ULID,
	opts InfoOpts,
) (FolderInfo, error) {
	f, err := uc.folderRepo.GetByID(ctx, folderID)
	if err != nil {
		return FolderInfo{}, fmt.Errorf("failed to get folder (%v, %v): %w",
			accountID, folderID, err)
	}

	return uc.fetchFolderInfo(ctx, f, opts)
}

func (uc *Usecase) FetchFolderInfo(
	ctx context.Context, accountID ulid.ULID, folderPath string,
	opts InfoOpts,
) (FolderInfo, error) {
	f, err := uc.folderRepo.GetByPath(ctx, accountID, folderPath)
	if err != nil {
		return FolderInfo{}, fmt.Errorf("failed to get folder (%v, %v): %w",
			accountID, folderPath, err)
	}

	return uc.fetchFolderInfo(ctx, f, opts)
}

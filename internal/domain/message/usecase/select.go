package messageusecase

import (
	"context"
	"fmt"
	"math"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/oklog/ulid/v2"
)

type FolderInfo struct {
	Folder      *folder.Folder
	UsedFlags   []string
	NumMessages uint32
	NumUnread   uint32
}

func (uc *Usecase) FetchFolderInfo(
	ctx context.Context, accountID ulid.ULID, folderPath string,
	flags, countMessages, countUnread bool,
) (FolderInfo, error) {
	f, err := uc.folderRepo.GetByPath(ctx, accountID, folderPath)
	if err != nil {
		return FolderInfo{}, fmt.Errorf("failed to get folder (%v, %v): %w",
			accountID, folderPath, err)
	}

	info := FolderInfo{
		Folder: f,
	}

	if countMessages {
		msgsCnt, err := uc.folderRepo.CountEntryByUIDRange(ctx, f.ID_, folder.UIDRange{Since: 1, Until: math.MaxUint32})
		if err != nil {
			return FolderInfo{}, fmt.Errorf("failed to count entries (%v): %w", f.ID_, err)
		}
		info.NumMessages = uint32(msgsCnt)
	}

	if flags {
		panic("implement me")
	}

	if countUnread {
		panic("implement me")
	}

	return info, nil
}

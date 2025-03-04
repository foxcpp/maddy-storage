package messageusecase

import (
	"context"

	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	"github.com/oklog/ulid/v2"
)

func (uc *Usecase) Search(
	ctx context.Context, accountID, folderID ulid.ULID,
	cond *searcher.Cond, opts searcher.Opts,
) (searcher.SearchResult, error) {
	if cond == nil {
		cond = &searcher.Cond{}
	}

	cond.FolderIDs = []ulid.ULID{folderID}

	return uc.searcher.Search(ctx, accountID, cond, opts)
}

package foldersqlite

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"runtime/trace"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/model"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/table"
	"github.com/go-jet/jet/v2/qrm"
	j "github.com/go-jet/jet/v2/sqlite"
	"github.com/oklog/ulid/v2"
)

func imapFolderAsModel(dto *model.ImapFolders) (folder.IMAPFolder, error) {
	id, err := ulidFromBlob(dto.FolderID)
	if err != nil {
		return folder.IMAPFolder{}, err
	}

	return folder.IMAPFolder{
		ID:          id,
		UIDNext:     uint32(dto.UIDNext),
		UIDValidity: uint32(dto.UIDValidity),
	}, nil
}

type IMAPRepo struct {
	db sqlite.DB
}

func NewIMAP(db sqlite.DB) IMAPRepo {
	return IMAPRepo{db: db}
}

func (r IMAPRepo) CreateIMAPFolders(ctx context.Context, ids ...ulid.ULID) ([]folder.IMAPFolder, error) {
	if len(ids) == 0 {
		return []folder.IMAPFolder{}, nil
	}

	dtos := make([]model.ImapFolders, len(ids))
	models := make([]folder.IMAPFolder, len(ids))
	for i, id := range ids {
		dtos[i] = model.ImapFolders{
			FolderID:    id[:],
			UIDNext:     1,
			UIDValidity: int64(rand.Uint32()),
		}
		models[i] = folder.IMAPFolder{
			ID:          id,
			UIDNext:     1,
			UIDValidity: uint32(dtos[i].UIDValidity),
		}
	}

	stmt := table.ImapFolders.INSERT(table.ImapFolders.AllColumns).MODELS(dtos)
	if _, err := r.db.Exec(ctx, stmt); err != nil {
		return nil, err
	}

	return models, nil
}

func (r IMAPRepo) GetIMAPFolder(ctx context.Context, id ulid.ULID) (folder.IMAPFolder, error) {
	var dto model.ImapFolders

	stmt := j.SELECT(table.ImapFolders.AllColumns).
		FROM(table.ImapFolders).
		WHERE(table.ImapFolders.FolderID.EQ(blob(id))).
		LIMIT(1)
	if err := r.db.Query(ctx, stmt, &dto); err != nil {
		if errors.Is(err, qrm.ErrNoRows) {
			return folder.IMAPFolder{}, folder.ErrNotFound
		}
		return folder.IMAPFolder{}, storeerrors.InternalError{Reason: fmt.Errorf("failed to fetch imap folder info: %w", err)}
	}

	m, err := imapFolderAsModel(&dto)
	if err != nil {
		return folder.IMAPFolder{}, storeerrors.InternalError{Reason: err}
	}
	return m, nil
}

func (r IMAPRepo) GetIMAPFolders(ctx context.Context, ids []ulid.ULID) ([]folder.IMAPFolder, error) {
	if len(ids) == 0 {
		return []folder.IMAPFolder{}, nil
	}

	var dtos []model.ImapFolders

	stmt := j.SELECT(table.ImapFolders.AllColumns).
		FROM(table.ImapFolders).
		WHERE(table.ImapFolders.FolderID.IN(blobs(ids)...))
	if err := r.db.Query(ctx, stmt, &dtos); err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("failed to fetch imap folder info: %w", err)}
	}

	models := make([]folder.IMAPFolder, len(dtos))
	for i := range dtos {
		m, err := imapFolderAsModel(&dtos[i])
		if err != nil {
			return nil, storeerrors.InternalError{Reason: err}
		}
		models[i] = m
	}

	return models, nil
}

func (r IMAPRepo) NextUID(ctx context.Context, folderID ulid.ULID, n int) ([]uint32, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.NextUID").End()

	if n <= 0 {
		panic("n must be positive")
	}

	var dto model.ImapFolders

	// Returns the value after increment, so the allocated range ends at
	// UIDNext-1.
	stmt := table.ImapFolders.UPDATE().
		SET(table.ImapFolders.UIDNext.SET(table.ImapFolders.UIDNext.ADD(j.Int(int64(n))))).
		WHERE(table.ImapFolders.FolderID.EQ(blob(folderID))).
		RETURNING(table.ImapFolders.UIDNext)
	if err := r.db.Query(ctx, stmt, &dto); err != nil {
		if errors.Is(err, qrm.ErrNoRows) {
			return nil, folder.ErrNotFound
		}
		return nil, storeerrors.InternalError{Reason: err}
	}

	lastUID := uint32(dto.UIDNext) - 1

	uids := make([]uint32, 0, n)
	for i := lastUID - uint32(n) + 1; i <= lastUID; i++ {
		uids = append(uids, i)
	}

	return uids, nil
}

func (r IMAPRepo) LastModSeq(ctx context.Context, accountID ulid.ULID) (folder.ModSeq, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.LastModSeq").End()

	var dto model.Modseq

	stmt := j.SELECT(table.Modseq.Modseq).
		FROM(table.Modseq).
		WHERE(table.Modseq.AccountID.EQ(blob(accountID))).
		LIMIT(1)
	if err := r.db.Query(ctx, stmt, &dto); err != nil {
		if errors.Is(err, qrm.ErrNoRows) {
			return 0, nil
		}
		return 0, storeerrors.InternalError{Reason: err}
	}

	return folder.ModSeq(dto.Modseq), nil
}

func (r IMAPRepo) NextModSeq(ctx context.Context, accountID ulid.ULID) (folder.ModSeq, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.NextModSeq").End()

	var dto model.Modseq

	stmt := table.Modseq.INSERT(table.Modseq.AccountID, table.Modseq.Modseq).
		VALUES(blob(accountID), j.Int(1)).
		ON_CONFLICT(table.Modseq.AccountID).
		DO_UPDATE(j.SET(table.Modseq.Modseq.SET(table.Modseq.Modseq.ADD(j.Int(1))))).
		RETURNING(table.Modseq.Modseq)
	if err := r.db.Query(ctx, stmt, &dto); err != nil {
		return 0, storeerrors.InternalError{Reason: err}
	}

	return folder.ModSeq(dto.Modseq), nil
}

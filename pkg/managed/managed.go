package managed

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/account"
	accountusecase "github.com/foxcpp/maddy-storage/internal/domain/account/usecase"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	folderusecase "github.com/foxcpp/maddy-storage/internal/domain/folder/usecase"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	messageusecase "github.com/foxcpp/maddy-storage/internal/domain/message/usecase"
	"github.com/oklog/ulid/v2"
)

var (
	ErrAccountNotFound = account.ErrNotFound
	ErrFolderNotFound  = folder.ErrNotFound
)

type AccountDTO struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

type AccountInfo struct {
	AccountDTO

	CustomAppendLimit uint32
	MaxStorageBytes   uint64
	MaxMessagesCount  uint32
}

type AccountOptions struct{}

type FolderDTO struct {
	ID   string
	Path string
	Role string
}

type FolderOptions struct {
	Role string
}

type MessageDTO struct {
	ID         string
	ReceivedAt time.Time
	Title      string
	Flags      []string
}

type Container struct {
	accounts *accountusecase.Account
	folders  *folderusecase.Folder
	message  *messageusecase.Usecase
}

func New(
	accounts *accountusecase.Account,
	folders *folderusecase.Folder,
	message *messageusecase.Usecase,
) *Container {
	return &Container{
		accounts: accounts,
		folders:  folders,
		message:  message,
	}
}

func (m Container) GetAccountInfo(ctx context.Context, username string) (*AccountInfo, error) {
	acct, err := m.accounts.GetByName(ctx, username)
	if err != nil {
		return nil, err
	}

	return &AccountInfo{
		AccountDTO: AccountDTO{
			ID:        acct.ID.String(),
			Name:      acct.Name,
			CreatedAt: acct.CreatedAt,
		},
		CustomAppendLimit: acct.Namespace.AppendLimit,
		MaxStorageBytes:   acct.Namespace.MaxStorageBytes,
		MaxMessagesCount:  acct.Namespace.MaxMessagesCount,
	}, nil
}

func (m Container) ListAccounts(ctx context.Context, substring string, limit, offset int) ([]AccountDTO, error) {
	accounts, err := m.accounts.ListAll(ctx)
	if err != nil {
		return nil, err
	}

	if substring != "" {
		filtered := make([]account.Account, 0, len(accounts))
		needle := strings.ToLower(substring)
		for _, acct := range accounts {
			if strings.Contains(strings.ToLower(acct.Name), needle) {
				filtered = append(filtered, acct)
			}
		}
		accounts = filtered
	}

	accounts, err = paginate(accounts, limit, offset)
	if err != nil {
		return nil, err
	}

	result := make([]AccountDTO, len(accounts))
	for i, acct := range accounts {
		result[i] = accountToDTO(acct)
	}

	return result, nil
}

func (m Container) CreateAccount(ctx context.Context, name string, options AccountOptions) (AccountDTO, error) {
	acct, err := m.accounts.Create(ctx, name)
	if err != nil {
		return AccountDTO{}, err
	}

	return accountToDTO(*acct), nil
}

func (m Container) DeleteAccount(ctx context.Context, name string) error {
	_, err := m.accounts.DeleteByName(ctx, name)
	return err
}

func (m Container) ListFolders(ctx context.Context, accountName string) ([]FolderDTO, error) {
	acct, err := m.accounts.GetByName(ctx, accountName)
	if err != nil {
		return nil, err
	}

	foldersData, err := m.folders.List(ctx, acct.ID, &folderusecase.ListOpts{
		SortAsTree: true,
	}, folder.OrderBySortOrder)
	if err != nil {
		return nil, err
	}

	result := make([]FolderDTO, len(foldersData))
	for i, data := range foldersData {
		result[i] = folderToDTO(data.Folder)
	}

	return result, nil
}

func (m Container) CreateFolder(ctx context.Context, accountName, folderPath string, options FolderOptions) (FolderDTO, error) {
	acct, err := m.accounts.GetByName(ctx, accountName)
	if err != nil {
		return FolderDTO{}, err
	}

	role, err := parseFolderRole(options.Role)
	if err != nil {
		return FolderDTO{}, err
	}

	created, err := m.folders.Create(ctx, acct.ID, folderPath, role, false)
	if err != nil {
		return FolderDTO{}, err
	}

	return folderToDTO(*created), nil
}

func (m Container) RenameFolder(ctx context.Context, accountName, oldPath, newPath string) error {
	acct, err := m.accounts.GetByName(ctx, accountName)
	if err != nil {
		return err
	}

	_, err = m.folders.Rename(ctx, acct.ID, oldPath, newPath, false)
	return err
}

func (m Container) DeleteFolder(ctx context.Context, accountName, path string) error {
	acct, err := m.accounts.GetByName(ctx, accountName)
	if err != nil {
		return err
	}

	foldersData, err := m.folders.List(ctx, acct.ID, &folderusecase.ListOpts{}, folder.OrderByName)
	if err != nil {
		return err
	}

	toDelete := make([]string, 0, len(foldersData))
	prefix := path + folder.PathSeparator
	for _, data := range foldersData {
		if data.Folder.Path == path || strings.HasPrefix(data.Folder.Path, prefix) {
			toDelete = append(toDelete, data.Folder.Path)
		}
	}
	if len(toDelete) == 0 {
		return folder.ErrNotFound
	}

	sort.Slice(toDelete, func(i, j int) bool {
		return strings.Count(toDelete[i], folder.PathSeparator) > strings.Count(toDelete[j], folder.PathSeparator)
	})

	for _, folderPath := range toDelete {
		if _, err := m.folders.Delete(ctx, acct.ID, false, folderPath); err != nil {
			return err
		}
	}

	return nil
}

func (m Container) ListMessages(ctx context.Context, accountName, folderPath string, limit, offset int) ([]MessageDTO, error) {
	acct, fold, err := m.resolveAccountFolder(ctx, accountName, folderPath)
	if err != nil {
		return nil, err
	}

	found, err := m.message.Search(ctx, acct.ID, fold.ID, nil, searcher.Opts{
		ReturnAll: true,
		Sort: []searcher.SortKey{{
			Field:   searcher.SortReceivedAt,
			Reverse: true,
		}},
	})
	if err != nil {
		return nil, err
	}

	entries, err := paginate(found.All, limit, offset)
	if err != nil {
		return nil, err
	}

	ids := make([]ulid.ULID, len(entries))
	for i, foundMsg := range entries {
		ids[i] = foundMsg.MessageID
	}

	return m.fetchMessageDTOs(ctx, ids)
}

func (m Container) AddMessage(ctx context.Context, accountName, folderPath string, date time.Time, r io.Reader) (MessageDTO, error) {
	acct, err := m.accounts.GetByName(ctx, accountName)
	if err != nil {
		return MessageDTO{}, err
	}

	raw, err := io.ReadAll(r)
	if err != nil {
		return MessageDTO{}, err
	}

	created, err := m.message.CreateMessage(ctx, acct.ID, folderPath, date, nil, int64(len(raw)), bytes.NewReader(raw))
	if err != nil {
		return MessageDTO{}, err
	}

	return messageToDTO(*created.Msg), nil
}

func (m Container) CopyMessages(ctx context.Context, accountName, folderPath, toFolderPath string, ids []string) ([]MessageDTO, error) {
	acct, fold, err := m.resolveAccountFolder(ctx, accountName, folderPath)
	if err != nil {
		return nil, err
	}

	msgIDs, err := parseMessageIDs(ids)
	if err != nil {
		return nil, err
	}

	found, err := m.findMessagesInFolder(ctx, acct.ID, fold.ID, msgIDs)
	if err != nil {
		return nil, err
	}

	uidRange := folder.Range{Values: make([]uint32, len(msgIDs))}
	for i, id := range msgIDs {
		uidRange.Values[i] = found[id].UID
	}

	copyData, err := m.message.Copy(ctx, acct.ID, uidRange, fold.ID, toFolderPath)
	if err != nil {
		return nil, err
	}

	targetIDs := make([]ulid.ULID, len(copyData.TargetEntries))
	for i, entry := range copyData.TargetEntries {
		targetIDs[i] = entry.MsgID
	}

	return m.fetchMessageDTOs(ctx, targetIDs)
}

func (m Container) MoveMessages(ctx context.Context, accountName, folderPath, toFolderPath string, ids []string) ([]MessageDTO, error) {
	acct, fold, err := m.resolveAccountFolder(ctx, accountName, folderPath)
	if err != nil {
		return nil, err
	}

	msgIDs, err := parseMessageIDs(ids)
	if err != nil {
		return nil, err
	}

	found, err := m.findMessagesInFolder(ctx, acct.ID, fold.ID, msgIDs)
	if err != nil {
		return nil, err
	}

	uidRange := folder.Range{Values: make([]uint32, len(msgIDs))}
	for i, id := range msgIDs {
		uidRange.Values[i] = found[id].UID
	}

	moveData, err := m.message.Move(ctx, acct.ID, uidRange, fold.ID, toFolderPath, false)
	if err != nil {
		return nil, err
	}

	targetIDs := make([]ulid.ULID, len(moveData.TargetEntries))
	for i, entry := range moveData.TargetEntries {
		targetIDs[i] = entry.MsgID
	}

	return m.fetchMessageDTOs(ctx, targetIDs)
}

func (m Container) DeleteMessages(ctx context.Context, accountName, folderPath string, ids []string) error {
	acct, fold, err := m.resolveAccountFolder(ctx, accountName, folderPath)
	if err != nil {
		return err
	}

	msgIDs, err := parseMessageIDs(ids)
	if err != nil {
		return err
	}

	if _, err := m.findMessagesInFolder(ctx, acct.ID, fold.ID, msgIDs); err != nil {
		return err
	}

	_, err = m.message.DeleteByIDs(ctx, acct.ID, fold.ID, msgIDs)
	return err
}

func (m Container) RemoveFlag(ctx context.Context, accountName, folderPath string, ids []string, flag string) error {
	acct, fold, err := m.resolveAccountFolder(ctx, accountName, folderPath)
	if err != nil {
		return err
	}

	msgIDs, err := parseMessageIDs(ids)
	if err != nil {
		return err
	}

	// This solely checks folderPath correctness as we don't need any folder information
	// to modify message flags. But we check it anyway to catch API misue.
	if _, err := m.findMessagesInFolder(ctx, acct.ID, fold.ID, msgIDs); err != nil {
		return err
	}

	return m.message.DeleteFlagsByIDs(ctx, acct.ID, msgIDs, []string{flag})
}

func (m Container) AddFlag(ctx context.Context, accountName, folderPath string, ids []string, flag string) error {
	acct, fold, err := m.resolveAccountFolder(ctx, accountName, folderPath)
	if err != nil {
		return err
	}

	msgIDs, err := parseMessageIDs(ids)
	if err != nil {
		return err
	}

	// This solely checks folderPath correctness as we don't need any folder information
	// to modify message flags. But we check it anyway to catch API misue.
	if _, err := m.findMessagesInFolder(ctx, acct.ID, fold.ID, msgIDs); err != nil {
		return err
	}

	return m.message.AddFlagsByIDs(ctx, acct.ID, msgIDs, []string{flag})
}

func (m Container) DumpMessage(ctx context.Context, accountName, folderPath string, id string) (io.ReadCloser, error) {
	msgID, err := ulid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("parse message id %q: %w", id, err)
	}

	msgs, err := m.message.FetchByIDs(ctx, []ulid.ULID{msgID})
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("message %s: not found", id)
	}

	reader, writer := io.Pipe()
	go func(msg message.Msg) {
		if err := m.message.WritePart(ctx, &msg, message.EmptyPath(), writer, messageusecase.WriteOptions{
			Specifier: messageusecase.PartDefault,
		}); err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		_ = writer.Close()
	}(msgs[0])

	return reader, nil
}

func accountToDTO(acct account.Account) AccountDTO {
	return AccountDTO{
		ID:        acct.ID.String(),
		Name:      acct.Name,
		CreatedAt: acct.CreatedAt,
	}
}

func folderToDTO(fold folder.Folder) FolderDTO {
	return FolderDTO{
		ID:   fold.ID.String(),
		Path: fold.Path,
		Role: string(fold.Role),
	}
}

func messageToDTO(msg message.Msg) MessageDTO {
	title := ""
	if msg.Content != nil && msg.Content.Envelope != nil {
		title = msg.Content.Envelope.Subject
	}

	return MessageDTO{
		ID:         msg.ID.String(),
		ReceivedAt: msg.ReceivedAt,
		Title:      title,
		Flags:      append([]string(nil), msg.Flags...),
	}
}

func parseFolderRole(role string) (folder.Role, error) {
	parsed := folder.Role(role)
	if parsed == folder.RoleNone {
		return parsed, nil
	}
	if !parsed.Valid() {
		return folder.RoleNone, fmt.Errorf("invalid folder role %q", role)
	}
	return parsed, nil
}

func paginate[T any](items []T, limit, offset int) ([]T, error) {
	if limit < 0 {
		return nil, fmt.Errorf("limit must be >= 0")
	}
	if offset < 0 {
		return nil, fmt.Errorf("offset must be >= 0")
	}
	if offset >= len(items) {
		return []T{}, nil
	}

	items = items[offset:]
	if limit == 0 || limit >= len(items) {
		return items, nil
	}
	return items[:limit], nil
}

func parseMessageIDs(ids []string) ([]ulid.ULID, error) {
	parsed := make([]ulid.ULID, len(ids))
	for i, id := range ids {
		parsedID, err := ulid.Parse(id)
		if err != nil {
			return nil, fmt.Errorf("parse message id %q: %w", id, err)
		}
		parsed[i] = parsedID
	}
	return parsed, nil
}

func (m Container) resolveAccountFolder(ctx context.Context, accountName, folderPath string) (*account.Account, *folder.Folder, error) {
	acct, err := m.accounts.GetByName(ctx, accountName)
	if err != nil {
		return nil, nil, err
	}

	fold, err := m.getFolderByPath(ctx, acct.ID, folderPath)
	if err != nil {
		return nil, nil, err
	}

	return acct, fold, nil
}

func (m Container) getFolderByPath(ctx context.Context, accountID ulid.ULID, folderPath string) (*folder.Folder, error) {
	foldersData, err := m.folders.List(ctx, accountID, &folderusecase.ListOpts{
		Filter: folder.Filter{
			Path: &folderPath,
		},
	}, folder.OrderByName)
	if err != nil {
		return nil, err
	}
	if len(foldersData) == 0 {
		return nil, folder.ErrNotFound
	}

	fold := foldersData[0].Folder
	return &fold, nil
}

func (m Container) findMessagesInFolder(ctx context.Context, accountID, folderID ulid.ULID, ids []ulid.ULID) (map[ulid.ULID]searcher.FoundMsg, error) {
	if len(ids) == 0 {
		return map[ulid.ULID]searcher.FoundMsg{}, nil
	}

	found, err := m.message.Search(ctx, accountID, folderID, nil, searcher.Opts{ReturnAll: true})
	if err != nil {
		return nil, err
	}

	byID := make(map[ulid.ULID]searcher.FoundMsg, len(found.All))
	for _, foundMsg := range found.All {
		byID[foundMsg.MessageID] = foundMsg
	}

	for _, id := range ids {
		if _, ok := byID[id]; !ok {
			return nil, fmt.Errorf("message %s is not present in folder", id)
		}
	}

	return byID, nil
}

func (m Container) fetchMessageDTOs(ctx context.Context, ids []ulid.ULID) ([]MessageDTO, error) {
	if len(ids) == 0 {
		return []MessageDTO{}, nil
	}

	msgs, err := m.message.FetchByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	byID := make(map[ulid.ULID]message.Msg, len(msgs))
	for _, msg := range msgs {
		byID[msg.ID] = msg
	}

	result := make([]MessageDTO, len(ids))
	for i, id := range ids {
		msg, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("message %s: not found", id)
		}
		result[i] = messageToDTO(msg)
	}

	return result, nil
}

package accountusecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/account"
	"github.com/foxcpp/maddy-storage/internal/domain/changelog"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlog"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type Auth interface {
	Login(ctx context.Context, username, password string) (string, error)
}

type StubAuth struct{}

func (s StubAuth) Login(_ context.Context, username, _ string) (string, error) {
	return username, nil
}

type InitFolder struct {
	Name string
	Role folder.Role
}

type Cfg struct {
	InitialFolders []InitFolder // INBOX is always created.
}

type Account struct {
	cfg        Cfg
	repo       account.Repo
	folderRepo folder.Repo
	imapRepo   folder.IMAPRepo
	auth       Auth
	changeLog  changelog.Repo
}

func NewAccount(
	cfg Cfg,
	repo account.Repo,
	folderRepo folder.Repo,
	imapRepo folder.IMAPRepo,
	auth Auth,
	changeLog changelog.Repo,
) Account {
	return Account{
		cfg:        cfg,
		repo:       repo,
		folderRepo: folderRepo,
		imapRepo:   imapRepo,
		auth:       auth,
		changeLog:  changeLog,
	}
}

func (a Account) GetByName(ctx context.Context, name string) (*account.Account, error) {
	return a.repo.GetByName(ctx, name)
}

func (a Account) ListAll(ctx context.Context) ([]account.Account, error) {
	return a.repo.GetAll(ctx, time.Time{}, account.OrderID)
}

func (a Account) Create(ctx context.Context, name string) (*account.Account, error) {
	acct, err := account.NewAccount(name)
	if err != nil {
		return nil, err
	}

	if err := a.repo.Create(ctx, acct); err != nil {
		return nil, fmt.Errorf("failed to create account: %w", err)
	}

	// IMAP requires INBOX folder to exist so always create one.
	folderIDs := make([]ulid.ULID, 0, len(a.cfg.InitialFolders)+1)
	inbox, err := folder.NewFolder(nil, acct.ID, "INBOX", folder.RoleInbox)
	if err != nil {
		return nil, err
	}
	folderIDs = append(folderIDs, inbox.ID)
	if err := a.folderRepo.Create(ctx, inbox); err != nil {
		return nil, fmt.Errorf("failed to create INBOX: %w", err)
	}
	for _, init := range a.cfg.InitialFolders {
		f, err := folder.NewFolder(nil, acct.ID, init.Name, init.Role)
		if err != nil {
			return nil, err
		}
		if err := a.folderRepo.Create(ctx, f); err != nil {
			return nil, fmt.Errorf("failed to create INBOX: %w", err)
		}
		folderIDs = append(folderIDs, f.ID)
	}
	if _, err := a.imapRepo.CreateIMAPFolders(ctx, folderIDs...); err != nil {
		return nil, fmt.Errorf("failed to create IMAP folders: %w", err)
	}

	contextlog.FromContext(ctx).Debug("account created",
		zap.Stringer("account_id", acct.ID), zap.String("name", name))

	return acct, nil
}

func (a Account) DeleteByName(ctx context.Context, name string) (ulid.ULID, error) {
	acct, err := a.repo.GetByName(ctx, name)
	if err != nil {
		return ulid.ULID{}, err
	}

	err = a.repo.Delete(ctx, acct.ID)
	if err != nil {
		return ulid.ULID{}, err
	}

	return acct.ID, nil
}

func (a Account) AccountNamespace(ctx context.Context, id ulid.ULID) (folder.Namespace, error) {
	// TODO: Caching, this method is going to be called a lot.

	acct, err := a.repo.GetByID(ctx, id)
	if err != nil {
		return folder.Namespace{}, err
	}
	return acct.Namespace, nil
}

func (a Account) AuthPlain(ctx context.Context, username, password string) (ulid.ULID, error) {
	authzUsername, err := a.auth.Login(ctx, username, password)
	if err != nil {
		return ulid.ULID{}, err
	}

	acct, err := a.repo.GetByName(ctx, authzUsername)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) {
			return ulid.ULID{}, ErrInvalidCredentials
		}
		return ulid.ULID{}, err
	}

	return acct.ID, nil
}

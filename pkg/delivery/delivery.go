package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/trace"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/account"
	accountusecase "github.com/foxcpp/maddy-storage/internal/domain/account/usecase"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	folderusecase "github.com/foxcpp/maddy-storage/internal/domain/folder/usecase"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	messageusecase "github.com/foxcpp/maddy-storage/internal/domain/message/usecase"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/oklog/ulid/v2"
)

var ErrUnknownRecipient = errors.New("no such recipient")

type Config struct{}

type Container struct {
	cfg    Config
	acct   *accountusecase.Account
	folder *folderusecase.Folder
	msg    *messageusecase.Usecase
}

func NewContainer(
	cfg Config,
	acct *accountusecase.Account,
	folder *folderusecase.Folder,
	msg *messageusecase.Usecase,
) *Container {
	return &Container{
		cfg:    cfg,
		acct:   acct,
		folder: folder,
		msg:    msg,
	}
}

func (c *Container) StartDelivery(ctx context.Context, id string) (*Delivery, error) {
	return &Delivery{
		c:  c,
		id: id,
	}, nil
}

type Delivery struct {
	c *Container

	id                 string
	folders            []folder.Folder
	additionalPreamble [][]byte
	flags              []string

	msg *message.NewMsg

	committed bool
}

type RcptOpts struct {
	PreferredRole      folder.Role
	AdditionalPremable []byte
}

func (d *Delivery) AddRcpt(
	ctx context.Context,
	accountName string,
	opts RcptOpts,
) error {
	ctx, task := trace.NewTask(ctx, "maddy-storage/delivery.AddRcpt")
	defer task.End()
	ctx = contextlib.WithAdditionalMeta(ctx, map[string]string{
		"delivery_id": d.id,
	})

	acct, err := d.c.acct.GetByName(ctx, accountName)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) {
			return ErrUnknownRecipient
		}
		return fmt.Errorf("AddRcpt %s: %w", accountName, err)
	}

	fold, err := d.c.folder.GetByRole(ctx, acct.ID, opts.PreferredRole, folder.FolderINBOX)
	if err != nil {
		return fmt.Errorf("AddRcpt %s: %w", accountName, err)
	}

	d.folders = append(d.folders, *fold)
	d.additionalPreamble = append(d.additionalPreamble, opts.AdditionalPremable)

	return nil
}

func (d *Delivery) SetFlags(flags []string) {
	d.flags = flags
}

func (d *Delivery) PrepareBody(ctx context.Context, size int64, r io.Reader) error {
	ctx, task := trace.NewTask(ctx, "maddy-storage/delivery.PrepareBody")
	defer task.End()
	ctx = contextlib.WithAdditionalMeta(ctx, map[string]string{
		"delivery_id": d.id,
	})

	if len(d.folders) == 0 {
		return fmt.Errorf("need at least one folder selected for PrepareBody")
	}

	var err error
	d.msg, err = d.c.msg.PrepareMessage(
		ctx, d.folders[0].AccountID, time.Now(),
		d.flags, size, r,
	)
	if err != nil {
		return fmt.Errorf("PrepareMessage: %w", err)
	}
	return nil
}

func (d *Delivery) Close(ctx context.Context) error {
	if d.committed {
		return nil
	}

	ctx, task := trace.NewTask(ctx, "maddy-storage/delivery.Close")
	defer task.End()
	ctx = contextlib.WithAdditionalMeta(ctx, map[string]string{
		"delivery_id": d.id,
	})

	if d.c.msg != nil {
		if err := d.c.msg.RemoveDanglingParts(ctx, d.msg); err != nil {
			return fmt.Errorf("RemoveDanglingParts: %w", err)
		}
	}
	d.committed = true
	return nil
}

func (d *Delivery) Commit(ctx context.Context) error {
	ctx, task := trace.NewTask(ctx, "maddy-storage/delivery.Close")
	defer task.End()
	ctx = contextlib.WithAdditionalMeta(ctx, map[string]string{
		"delivery_id": d.id,
	})

	if len(d.folders) == 0 {
		return d.Close(ctx)
	}
	if d.msg == nil {
		panic("PrepareBody was not called")
	}
	if d.committed {
		panic("Cannot commit a delivery twice")
	}

	// There is always part 0.
	originalPart0Content := d.msg.Parts[0].Content
	originalPart0Blob := d.msg.Parts[0].InlineBlob

	for i, fldr := range d.folders {
		msg := d.msg

		if i != 0 {
			// Store as copy - generate a new ID for each subsequent
			// folder used.
			msg.ID = ulid.Make()
			for i := range msg.Parts {
				msg.Parts[i].ID = ulid.Make()
			}
		}

		if d.additionalPreamble[i] != nil {
			preamble := d.additionalPreamble[i]
			msg.Parts[0].Content.HeaderSize = originalPart0Content.HeaderSize + uint32(len(preamble))
			msg.Parts[0].Content.HeaderLines += originalPart0Content.HeaderLines + int64(bytes.Count(preamble, []byte("\n")))

			inlineBlob := append([]byte{}, preamble...)
			inlineBlob = append(inlineBlob, originalPart0Blob...)
			msg.Parts[0].InlineBlob = inlineBlob
		}

		_, err := d.c.msg.AddMessageToFolder(
			ctx, fldr.AccountID,
			msg, &fldr,
		)
		if err != nil {
			return fmt.Errorf("AddMessageToFolder %v: %w", fldr.ID, err)
		}
	}

	d.committed = true
	return nil
}

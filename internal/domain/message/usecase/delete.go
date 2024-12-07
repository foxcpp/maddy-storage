package messageusecase

import (
	"context"

	"github.com/foxcpp/maddy-storage/internal/domain/message"
)

func (uc *Usecase) deleteNewPart(ctx context.Context, p *message.NewPart) error {
	if p.ExternalID != "" {
		return uc.blobStore.Delete(ctx, p.ExternalID)
	}
	return nil
}

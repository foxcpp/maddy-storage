package contextlib

import (
	"context"

	"github.com/foxcpp/maddy-storage/internal/domain/metadata"
)

type mdKey struct{}

var mdKeyVal mdKey

func WithAdditionalMeta(ctx context.Context, md metadata.Md) context.Context {
	currentValue, ok := ctx.Value(mdKeyVal).(metadata.Md)
	if !ok {
		currentValue = metadata.Md{}
	}

	for k, v := range md {
		if v == "" {
			delete(currentValue, k)
		}
		currentValue[k] = v
	}

	return context.WithValue(ctx, mdKeyVal, currentValue)
}

func GetAdditionalMeta(ctx context.Context) metadata.Md {
	currentValue, ok := ctx.Value(mdKeyVal).(metadata.Md)
	if !ok {
		return nil
	}
	return currentValue
}

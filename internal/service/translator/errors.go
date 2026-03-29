package translator

import "errors"

var (
	ErrRateLimited        = errors.New("model rate limited")
	ErrAllModelsExhausted = errors.New("all models exhausted for today")
)

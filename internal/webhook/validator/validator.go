package validator

import (
	"github.com/pkg/errors"

	"github.com/negz/hal5d/internal/webhook"
)

// A Validator wraps a webhook to satisfy cert.Validator.
type Validator struct {
	h webhook.Hook
}

// New creates a new Validator.
func New(h webhook.Hook) *Validator {
	return &Validator{h: h}
}

// Validate triggers the wrapped webhook, returning its error.
func (s *Validator) Validate() error {
	return errors.Wrap(s.h.Trigger(), "validation webhook failed")
}

package subscriber

import (
	"github.com/negz/hal5d/internal/webhook"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// A Subscriber wraps a webhook to satisfy cert.Subscriber.
type Subscriber struct {
	log *zap.Logger
	h   webhook.Hook
}

// An Option can be used to configure new Subscribers.
type Option func(*Subscriber) error

// WithLogger configures a Susbcribers's logger.
func WithLogger(l *zap.Logger) Option {
	return func(s *Subscriber) error {
		s.log = l
		return nil
	}
}

// New creates a new Subscriber.
func New(h webhook.Hook, o ...Option) (*Subscriber, error) {
	s := &Subscriber{log: zap.NewNop(), h: h}
	for _, so := range o {
		if err := so(s); err != nil {
			return nil, errors.Wrap(err, "cannot apply subscriber option")
		}
	}
	return s, nil
}

// Changed triggers the wrapped webhook asynchronously.
func (s *Subscriber) Changed() {
	go func() {
		if err := s.h.Trigger(); err != nil {
			s.log.Error("subscriber webhook failed", zap.Error(err))
		}
	}()
}

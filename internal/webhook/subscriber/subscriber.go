/*
Copyright 2018 Planet Labs Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
implied. See the License for the specific language governing permissions
and limitations under the License.
*/

package subscriber

import (
	"github.com/planetlabs/hal5d/internal/webhook"

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

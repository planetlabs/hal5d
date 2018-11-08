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

package validator

import (
	"errors"
	"testing"

	"github.com/negz/hal5d/internal/cert"
	"github.com/negz/hal5d/internal/webhook"
)

type predictableHook struct {
	err error
}

func (h *predictableHook) Trigger() error {
	return h.err
}

func TestValidator(t *testing.T) {
	var _ cert.Validator = &Validator{}

	cases := []struct {
		name    string
		h       webhook.Hook
		wantErr bool
	}{
		{name: "Success", h: &predictableHook{}, wantErr: false},
		{name: "Error", h: &predictableHook{errors.New("boom")}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := New(tc.h).Validate()
			if tc.wantErr && err == nil {
				t.Errorf("New(%v).Validate(): want error, got nil", tc.h)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("New(%v).Validate(): want no error, got %v", tc.h, err)
			}
		})
	}
}

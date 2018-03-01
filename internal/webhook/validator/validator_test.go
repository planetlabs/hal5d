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

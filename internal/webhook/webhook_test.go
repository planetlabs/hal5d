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

package webhook

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebhook(t *testing.T) {

	var _ Hook = &Webhook{}

	cases := []struct {
		name    string
		fn      http.HandlerFunc
		wantErr bool
	}{
		{
			name: "Success",
			fn: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				r.Body.Close()
			},
			wantErr: false,
		},
		{
			name: "Error",
			fn: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("Boom!"))
				r.Body.Close()
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(tc.fn)
			defer s.Close()

			err := New(s.URL).Trigger()
			if tc.wantErr && err == nil {
				t.Errorf("New(%v).Trigger(): want error, got nil", s.URL)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("New(%v).Trigger(): want no error, got %v", s.URL, err)
			}
		})
	}
}

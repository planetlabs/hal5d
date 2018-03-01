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

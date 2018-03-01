package webhook

import (
	"io/ioutil"
	"net/http"

	"github.com/pkg/errors"
)

// A Hook triggers an action in an external process by making an HTTP request.
type Hook interface {
	// Trigger the hook.
	Trigger() error
}

// A Webhook triggers an action in an external process by making a basic HTTP
// GET request with no parameters or request body.
type Webhook struct {
	url string
}

// New creates a new Webhook.
func New(url string) *Webhook {
	return &Webhook{url: url}
}

// Trigger sends an HTTP GET to the webhook's URL. Trigger returns an error when
// it encounters any HTTP status code that is not a 200 OK.
func (h *Webhook) Trigger() error {
	rsp, err := http.Get(h.url)
	if err != nil {
		return errors.Wrapf(err, "cannot trigger webhook URL %v", h.url)
	}
	defer rsp.Body.Close()

	if rsp.StatusCode == http.StatusOK {
		return nil
	}

	b, err := ioutil.ReadAll(rsp.Body)
	if err != nil {
		return errors.Wrapf(err, "cannot read webhook response")
	}
	return errors.Errorf("webhook failed: %d %v: %s", rsp.StatusCode, rsp.Status, b)
}

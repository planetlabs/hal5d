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

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

package kubernetes

import (
	"fmt"
	"testing"

	"github.com/go-test/deep"
	"github.com/pkg/errors"
	"k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	"k8s.io/client-go/tools/cache"
)

const (
	ns   = "namespace"
	name = "name"
)

type getByKeyFunc func(key string) (interface{}, bool, error)

type predictableInformer struct {
	cache.SharedInformer
	fn getByKeyFunc
}

func (i *predictableInformer) GetStore() cache.Store {
	return &cache.FakeCustomStore{GetByKeyFunc: i.fn}
}

func TestIngressWatcher(t *testing.T) {
	cases := []struct {
		name    string
		fn      getByKeyFunc
		want    *v1beta1.Ingress
		wantErr bool
	}{
		{
			name: "IngressExists",
			fn: func(k string) (interface{}, bool, error) {
				return &v1beta1.Ingress{}, true, nil
			},
			want: &v1beta1.Ingress{},
		},
		{
			name: "IngressDoesNotExist",
			fn: func(k string) (interface{}, bool, error) {
				return nil, false, nil
			},
			wantErr: true,
		},
		{
			name: "ErrorGettingIngress",
			fn: func(k string) (interface{}, bool, error) {
				return nil, false, errors.New("boom")
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i := &predictableInformer{fn: tc.fn}
			w := &IngressWatch{i}
			got, err := w.Get(ns, name)
			if err != nil {
				if tc.wantErr {
					return
				}
				t.Errorf("w.Get(%v, %v): %v", ns, name, err)
			}

			if diff := deep.Equal(tc.want, got); diff != nil {
				t.Errorf("w.Get(%v, %v): want != got %v", ns, name, diff)
			}
		})
	}
}
func TestSecretWatcher(t *testing.T) {
	cases := []struct {
		name    string
		fn      getByKeyFunc
		want    *v1.Secret
		wantErr string
	}{
		{
			name: "SecretExists",
			fn: func(k string) (interface{}, bool, error) {
				return &v1.Secret{}, true, nil
			},
			want: &v1.Secret{},
		},
		{
			name: "SecretDoesNotExist",
			fn: func(k string) (interface{}, bool, error) {
				return nil, false, nil
			},
			wantErr: "secret does not exist",
		},
		{
			name: "ErrorGettingSecret",
			fn: func(k string) (interface{}, bool, error) {
				return nil, false, errors.New("boom")
			},
			wantErr: fmt.Sprintf("cannot get secret %v/%v: %v", ns, name, "boom"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i := &predictableInformer{fn: tc.fn}
			w := &SecretWatch{i}
			got, err := w.Get(ns, name)
			if err != nil {
				if tc.wantErr != "" && err.Error() == tc.wantErr {
					return
				}
				t.Errorf("w.Get(%v, %v): %v", ns, name, err)
			}

			if diff := deep.Equal(tc.want, got); diff != nil {
				t.Errorf("w.Get(%v, %v): want != got %v", ns, name, diff)
			}
		})
	}
}

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

package cert

import (
	"bytes"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/planetlabs/hal5d/internal/kubernetes"

	"github.com/go-test/deep"
	"github.com/pkg/errors"
	"github.com/spf13/afero"
	"k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	coolIngress = &v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "coolIngress"},
		Spec:       v1beta1.IngressSpec{TLS: []v1beta1.IngressTLS{{SecretName: coolSecret.GetName()}}},
	}
	coolSecret = &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "coolSecret"},
		Data: map[string][]byte{
			v1.TLSCertKey:       []byte("cert"),
			v1.TLSPrivateKeyKey: []byte("key"),
		},
	}
	coolSecretWithoutCert = &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "coolSecret"},
		Data: map[string][]byte{
			v1.TLSPrivateKeyKey: []byte("key"),
		},
	}
	coolSecretWithoutKey = &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "coolSecret"},
		Data: map[string][]byte{
			v1.TLSCertKey: []byte("cert"),
		},
	}
	dankSecret = &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "dankSecret"},
		Data: map[string][]byte{
			v1.TLSCertKey:       []byte("dankcert"),
			v1.TLSPrivateKeyKey: []byte("dankkey"),
		},
	}
)

func TestErr(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		tester func(error) bool
		want   bool
	}{
		{
			name:   "FmtIsInvalid",
			err:    ErrInvalid(fmt.Errorf("kaboom")),
			tester: IsInvalid,
			want:   true,
		},
		{
			name:   "ErrorsIsInvalid",
			err:    ErrInvalid(errors.New("kaboom")),
			tester: IsInvalid,
			want:   true,
		},
		{
			name:   "WrappedIsInvalid",
			err:    errors.Wrap(ErrInvalid(errors.New("kaboom")), "full"),
			tester: IsInvalid,
			want:   true,
		},
		{
			name:   "NotInvalid",
			err:    errors.New("kaboom"),
			tester: IsInvalid,
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.tester(tc.err)
			if got != tc.want {
				t.Errorf("%v: got %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestNewCertPair(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		want     certPair
		wantErr  bool
	}{
		{
			name:     "ValidFilename",
			filename: "ns-ingress-secret.pem",
			want:     certPair{Namespace: "ns", IngressName: "ingress", SecretName: "secret"},
		},
		{
			name:     "InvalidSuffix",
			filename: "ns-ingress-secret.crt",
			wantErr:  true,
		},
		{
			name:     "InvalidParts",
			filename: "ingress-secret.pem",
			wantErr:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := newCertPair(tc.filename)
			if err != nil {
				if tc.wantErr {
					return
				}
				t.Errorf("newCertPair(%v): %v", tc.filename, err)
			}
			if diff := deep.Equal(tc.want, got); diff != nil {
				t.Errorf("newCertPair(%v): want != got %v", tc.filename, diff)
			}
		})
	}
}

type pessimisticValidator struct{}

func (v *pessimisticValidator) Validate() error {
	return errors.New("this config sucks")
}

type testSubscriber struct {
	notified int
}

func (s *testSubscriber) Changed() {
	s.notified++
}

func populate(t *testing.T, fs afero.Fs, files map[string][]byte) string {
	dir, err := afero.TempDir(fs, "/", "tls")
	if err != nil {
		t.Fatalf("cannot make temp dir: %v", err)
	}
	for name, data := range files {
		path := filepath.Join(dir, name)
		if err := afero.WriteFile(fs, path, data, 0600); err != nil {
			t.Fatalf("cannot write file :%v", err)
		}
	}
	return dir
}

func validate(t *testing.T, fs afero.Fs, dir string, wantFile map[string][]byte) {
	fi, err := afero.ReadDir(fs, dir)
	if err != nil {
		t.Fatalf("cannot list TLS cert pairs: %v", err)
	}
	seen := make(map[string]bool)
	for _, f := range fi {
		seen[f.Name()] = true
		want, ok := wantFile[f.Name()]
		if !ok {
			t.Errorf("found unwanted file: %v", f.Name())
		}
		got, err := afero.ReadFile(fs, filepath.Join(dir, f.Name()))
		if err != nil {
			t.Errorf("cannot read file: %v", err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%v: want content '%s', got '%s'", f.Name(), want, got)
		}
	}

	for f := range wantFile {
		if !seen[f] {
			t.Errorf("did not find wanted file: %v", f)
		}
	}
}

type mapSecretStore map[metadata]*v1.Secret

func (m mapSecretStore) Get(namespace, name string) (*v1.Secret, error) {
	md := metadata{Namespace: namespace, Name: name}
	s, ok := m[md]
	if !ok {
		return nil, errors.New("no such secret")
	}
	return s, nil
}

func TestUpsertIngress(t *testing.T) {
	cases := []struct {
		name     string
		i        *v1beta1.Ingress
		s        kubernetes.SecretStore
		v        Validator
		existing map[string][]byte
		want     map[string][]byte
	}{
		{
			name: "AddToEmptyDir",
			i:    coolIngress,
			s: mapSecretStore{
				metadata{Namespace: coolSecret.GetNamespace(), Name: coolSecret.GetName()}: coolSecret,
			},
			v: &optimisticValidator{},
			want: map[string][]byte{
				"ns-coolIngress-coolSecret.pem": []byte("cert\nkey"),
			},
		},
		{
			name: "AddToPopulatedDir",
			i:    coolIngress,
			s: mapSecretStore{
				metadata{Namespace: coolSecret.GetNamespace(), Name: coolSecret.GetName()}: coolSecret,
			},
			v: &optimisticValidator{},
			existing: map[string][]byte{
				"ns-anotherIngress-existingSecret.pem":     []byte("cert\nkey2"),
				"dankCert.pem":                             []byte("sodank"),
				"anotherns-coolIngress-existingSecret.pem": []byte("cert\nkey3"),
			},
			want: map[string][]byte{
				"ns-anotherIngress-existingSecret.pem":     []byte("cert\nkey2"),
				"ns-coolIngress-coolSecret.pem":            []byte("cert\nkey"),
				"dankCert.pem":                             []byte("sodank"),
				"anotherns-coolIngress-existingSecret.pem": []byte("cert\nkey3"),
			},
		},
		{
			name: "CertRemovedFromIngress",
			i:    coolIngress,
			s: mapSecretStore{
				metadata{Namespace: coolSecret.GetNamespace(), Name: coolSecret.GetName()}: coolSecret,
			},
			v: &optimisticValidator{},
			existing: map[string][]byte{
				"ns-coolIngress-existingSecret.pem": []byte("cert\nkey1"),
			},
			want: map[string][]byte{
				"ns-coolIngress-coolSecret.pem": []byte("cert\nkey"),
			},
		},
		{
			name: "OverwriteExistingCert",
			i:    coolIngress,
			s: mapSecretStore{
				metadata{Namespace: coolSecret.GetNamespace(), Name: coolSecret.GetName()}: coolSecret,
			},
			v: &optimisticValidator{},
			existing: map[string][]byte{
				"ns-coolIngress-coolSecret.pem": []byte("suchcert\nverykey"),
			},
			want: map[string][]byte{
				"ns-coolIngress-coolSecret.pem": []byte("cert\nkey"),
			},
		},
		{
			name: "UnchangedExistingCert",
			i:    coolIngress,
			s: mapSecretStore{
				metadata{Namespace: coolSecret.GetNamespace(), Name: coolSecret.GetName()}: coolSecret,
			},
			v: &optimisticValidator{},
			existing: map[string][]byte{
				"ns-coolIngress-coolSecret.pem": []byte("cert\nkey"),
			},
			want: map[string][]byte{
				"ns-coolIngress-coolSecret.pem": []byte("cert\nkey"),
			},
		},
		{
			name: "MissingSecret",
			i:    coolIngress,
			s:    mapSecretStore{},
			v:    &optimisticValidator{},
		},
		{
			name: "MissingSecretCert",
			i:    coolIngress,
			s: mapSecretStore{
				metadata{Namespace: coolSecret.GetNamespace(), Name: coolSecret.GetName()}: coolSecretWithoutCert,
			},
			v: &optimisticValidator{},
		},
		{
			name: "MissingSecretKey",
			i:    coolIngress,
			s: mapSecretStore{
				metadata{Namespace: coolSecret.GetNamespace(), Name: coolSecret.GetName()}: coolSecretWithoutKey,
			},
			v: &optimisticValidator{},
		},
		{
			name: "ValidationFails",
			i:    coolIngress,
			s: mapSecretStore{
				metadata{Namespace: coolSecret.GetNamespace(), Name: coolSecret.GetName()}: coolSecret,
			},
			v: &pessimisticValidator{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			dir := populate(t, fs, tc.existing)

			sub := &testSubscriber{}
			m, err := NewManager(dir, tc.s, WithFilesystem(fs), WithValidator(tc.v), WithSubscriber(sub))
			if err != nil {
				t.Fatalf("NewManager(...): %v", err)
			}

			m.OnUpdate(&v1beta1.Ingress{}, tc.i)
			got, want := sub.notified == 1, !reflect.DeepEqual(tc.existing, tc.want)
			if got != want {
				t.Errorf("m.OnAdd(...): changed directory content: want %v, got %v", want, got)
			}
			validate(t, fs, dir, tc.want)
		})
	}
}

func TestUpsertSecret(t *testing.T) {
	cases := []struct {
		name        string
		i           *v1beta1.Ingress
		s           *v1.Secret
		st          kubernetes.SecretStore
		v           Validator
		want        map[string][]byte
		wantChanges int
	}{
		{
			name: "AddSecretAfterIngress",
			i:    coolIngress,
			s:    coolSecret,
			st:   mapSecretStore{},
			v:    &optimisticValidator{},
			want: map[string][]byte{
				"ns-coolIngress-coolSecret.pem": []byte("cert\nkey"),
			},
			wantChanges: 1,
		},
		{
			name: "SecretDataUpdated",
			i:    coolIngress,
			s:    coolSecret,
			st: mapSecretStore{
				metadata{Namespace: coolSecret.GetNamespace(), Name: coolSecret.GetName()}: &v1.Secret{
					ObjectMeta: metav1.ObjectMeta{Namespace: coolSecret.GetNamespace(), Name: coolSecret.GetName()},
					Data: map[string][]byte{
						v1.TLSCertKey:       []byte("oldcert"),
						v1.TLSPrivateKeyKey: []byte("oldkey"),
					},
				},
			},
			v: &optimisticValidator{},
			want: map[string][]byte{
				"ns-coolIngress-coolSecret.pem": []byte("cert\nkey"),
			},
			wantChanges: 2,
		},
		{
			name: "SecretDataUnchanged",
			i:    coolIngress,
			s:    coolSecret,
			st: mapSecretStore{
				metadata{Namespace: coolSecret.GetNamespace(), Name: coolSecret.GetName()}: coolSecret,
			},
			v: &optimisticValidator{},
			want: map[string][]byte{
				"ns-coolIngress-coolSecret.pem": []byte("cert\nkey"),
			},
			wantChanges: 1,
		},
		{
			name:        "SecretDataMissingCert",
			i:           coolIngress,
			s:           coolSecretWithoutCert,
			st:          mapSecretStore{},
			v:           &optimisticValidator{},
			wantChanges: 0,
		},
		{
			name:        "SecretDataMissingKey",
			i:           coolIngress,
			s:           coolSecretWithoutKey,
			st:          mapSecretStore{},
			v:           &optimisticValidator{},
			wantChanges: 0,
		},
		{
			name:        "ValidationFails",
			i:           coolIngress,
			s:           coolSecret,
			st:          mapSecretStore{},
			v:           &pessimisticValidator{},
			wantChanges: 0,
		},
		{
			name: "UnreferencedSecret",
			i:    coolIngress,
			s:    dankSecret,
			st: mapSecretStore{
				metadata{Namespace: coolSecret.GetNamespace(), Name: coolSecret.GetName()}: coolSecret,
			},
			v: &optimisticValidator{},
			want: map[string][]byte{
				"ns-coolIngress-coolSecret.pem": []byte("cert\nkey"),
			},
			wantChanges: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			dir := populate(t, fs, nil)

			sub := &testSubscriber{}
			m, err := NewManager(dir, tc.st, WithFilesystem(fs), WithValidator(tc.v), WithSubscriber(sub))
			if err != nil {
				t.Fatalf("NewManager(...): %v", err)
			}

			m.OnAdd(tc.i)
			m.OnAdd(tc.s)
			got, want := sub.notified, tc.wantChanges
			if got != want {
				t.Errorf("m.OnAdd(...): changed directory content: want %v changes, got %v", want, got)
			}
			validate(t, fs, dir, tc.want)
		})
	}
}

func TestDeleteIngress(t *testing.T) {
	cases := []struct {
		name     string
		i        *v1beta1.Ingress
		s        kubernetes.SecretStore
		existing map[string][]byte
		want     map[string][]byte
	}{
		{
			name: "DeleteOnlyIngress",
			i:    coolIngress,
			existing: map[string][]byte{
				"ns-coolIngress-coolSecret.pem": []byte("cert\nkey"),
				"ns-coolIngress-dankSecret.pem": []byte("anothercert\nanotherkey"),
			},
		},
		{
			name: "DeleteUnknownIngress",
			i:    coolIngress,
			existing: map[string][]byte{
				"anotherns-coolIngress-coolSecret.pem": []byte("cert\nkey"),
			},
			want: map[string][]byte{
				"anotherns-coolIngress-coolSecret.pem": []byte("cert\nkey"),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			dir := populate(t, fs, tc.existing)

			sub := &testSubscriber{}
			m, err := NewManager(dir, tc.s, WithFilesystem(fs), WithSubscriber(sub))
			if err != nil {
				t.Fatalf("NewManager(...): %v", err)
			}

			m.OnDelete(tc.i)
			got, want := sub.notified == 1, !reflect.DeepEqual(tc.existing, tc.want)
			if got != want {
				t.Errorf("m.OnDelete(...): changed directory content: want %v, got %v", want, got)
			}
			validate(t, fs, dir, tc.want)
		})
	}
}

func TestDeleteSecret(t *testing.T) {
	cases := []struct {
		name        string
		i           *v1beta1.Ingress
		s           *v1.Secret
		st          kubernetes.SecretStore
		want        map[string][]byte
		wantChanges int
	}{
		{
			name: "DeleteSecret",
			i:    coolIngress,
			s:    coolSecret,
			st: mapSecretStore{
				metadata{Namespace: coolSecret.GetNamespace(), Name: coolSecret.GetName()}: coolSecret,
			},
			wantChanges: 2,
		},
		{
			name: "DeleteSecret",
			i:    coolIngress,
			s:    dankSecret,
			st: mapSecretStore{
				metadata{Namespace: coolSecret.GetNamespace(), Name: coolSecret.GetName()}: coolSecret,
			},
			want: map[string][]byte{
				"ns-coolIngress-coolSecret.pem": []byte("cert\nkey"),
			},
			wantChanges: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			dir := populate(t, fs, nil)

			sub := &testSubscriber{}
			m, err := NewManager(dir, tc.st, WithFilesystem(fs), WithSubscriber(sub))
			if err != nil {
				t.Fatalf("NewManager(...): %v", err)
			}

			m.OnAdd(tc.i)
			m.OnDelete(tc.s)
			got, want := sub.notified, tc.wantChanges
			if got != want {
				t.Errorf("m.OnDelete(...): changed directory content: want %v changes, got %v", want, got)
			}
			validate(t, fs, dir, tc.want)
		})
	}
}

func TestUpsertDeleteUpsertSecret(t *testing.T) {
	fs := afero.NewMemMapFs()
	dir := populate(t, fs, nil)

	sub := &testSubscriber{}
	m, err := NewManager(dir, mapSecretStore{}, WithFilesystem(fs), WithSubscriber(sub))
	if err != nil {
		t.Fatalf("NewManager(...): %v", err)
	}

	m.OnAdd(coolIngress)
	m.OnAdd(coolSecret)
	m.OnDelete(coolSecret)
	m.OnAdd(coolSecret)
	validate(t, fs, dir, map[string][]byte{
		"ns-coolIngress-coolSecret.pem": []byte("cert\nkey"),
	})
}

func TestUpsertDeleteUpsertIngress(t *testing.T) {
	fs := afero.NewMemMapFs()
	dir := populate(t, fs, nil)

	st := mapSecretStore{
		metadata{Namespace: coolSecret.GetNamespace(), Name: coolSecret.GetName()}: coolSecret,
	}
	sub := &testSubscriber{}
	m, err := NewManager(dir, st, WithFilesystem(fs), WithSubscriber(sub))
	if err != nil {
		t.Fatalf("NewManager(...): %v", err)
	}

	m.OnAdd(coolIngress)
	m.OnDelete(coolIngress)
	m.OnAdd(coolIngress)
	validate(t, fs, dir, map[string][]byte{
		"ns-coolIngress-coolSecret.pem": []byte("cert\nkey"),
	})
}

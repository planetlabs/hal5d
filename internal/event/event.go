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

package event

import (
	"github.com/negz/hal5d/internal/kubernetes"
	"k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
)

const (
	eventCertPairWritten  = "CertPairWritten"
	eventCertPairDeleted  = "CertPairDeleted"
	eventTLSSecretInvalid = "TLSSecretInvalid"
)

// A Recorder records events.
type Recorder interface {
	// NewWrite records the writing of a certificate pair.
	NewWrite(namespace, ingressName, secretName string)

	// NewDelete records the deletion of a certificate pair.
	NewDelete(namespace, ingressName, secretName string)

	// NewInvalidSecret records an invalid TLS secret.
	NewInvalidSecret(namespace, ingressName, secretName string)
}

// A NopRecorder does nothing.
type NopRecorder struct{}

// NewWrite does nothing.
func (r *NopRecorder) NewWrite(namespace, ingressName, secretName string) {}

// NewDelete does nothing.
func (r *NopRecorder) NewDelete(namespace, ingressName, secretName string) {}

// NewInvalidSecret does nothing.
func (r *NopRecorder) NewInvalidSecret(namespace, ingressName, secretName string) {}

// A KubernetesRecorder records events to Kubernetes.
type KubernetesRecorder struct {
	e record.EventRecorder
	i kubernetes.IngressStore
}

// NewKubernetesRecorder returns a Recorder that records events to Kubernetes.
func NewKubernetesRecorder(e record.EventRecorder, i kubernetes.IngressStore) *KubernetesRecorder {
	return &KubernetesRecorder{e: e, i: i}
}

// NewWrite records the writing of a certificate pair as an event on the
// supplied ingress.
func (r *KubernetesRecorder) NewWrite(namespace, ingressName, secretName string) {
	i, err := r.i.Get(namespace, ingressName)
	if err != nil {
		return
	}
	r.e.Eventf(i, v1.EventTypeNormal, eventCertPairWritten, "Loaded TLS certificate from secret %s", secretName)
}

// NewDelete records the deletion of a certificate pair as an event on the
// supplied ingress.
func (r *KubernetesRecorder) NewDelete(namespace, ingressName, secretName string) {
	i, err := r.i.Get(namespace, ingressName)
	if err != nil {
		return
	}
	r.e.Eventf(i, v1.EventTypeNormal, eventCertPairDeleted, "Unloaded TLS certificate from secret %s", secretName)
}

// NewInvalidSecret records an invalid TLS secret as an event on the supplied ingress.
func (r *KubernetesRecorder) NewInvalidSecret(namespace, ingressName, secretName string) {
	i, err := r.i.Get(namespace, ingressName)
	if err != nil {
		return
	}
	r.e.Eventf(i, v1.EventTypeWarning, eventTLSSecretInvalid, "Could not load TLS certificate from invalid secret %s", secretName)
}

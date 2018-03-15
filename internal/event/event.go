package event

import (
	"github.com/negz/hal5d/internal/kubernetes"
	"k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
)

const (
	eventCertPairWritten = "CertPairWritten"
	eventCertPairDeleted = "CertPairDeleted"
	eventCertPairInvalid = "CertPairInvalid"
)

// A Recorder records events.
type Recorder interface {
	// NewWrite records the writing of a certificate pair.
	NewWrite(namespace, ingressName, secretName string)

	// NewDelete records the deletion of a certificate pair.
	NewDelete(namespace, ingressName, secretName string)

	// NewInvalid records an invalid certificate pair.
	NewInvalid(namespace, ingressName, secretName string)
}

// A NopRecorder does nothing.
type NopRecorder struct{}

// NewWrite does nothing.
func (r *NopRecorder) NewWrite(namespace, ingressName, secretName string) {}

// NewDelete does nothing.
func (r *NopRecorder) NewDelete(namespace, ingressName, secretName string) {}

// NewInvalid does nothing.
func (r *NopRecorder) NewInvalid(namespace, ingressName, secretName string) {}

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

// NewInvalid records an invalid certificate pair as an event on the supplied
// ingress.
func (r *KubernetesRecorder) NewInvalid(namespace, ingressName, secretName string) {
	i, err := r.i.Get(namespace, ingressName)
	if err != nil {
		return
	}
	r.e.Eventf(i, v1.EventTypeWarning, eventCertPairInvalid, "Could not load invalid TLS certificate from secret %s", secretName)
}

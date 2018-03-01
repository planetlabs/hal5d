package kubernetes

import (
	"fmt"
	"time"

	"github.com/pkg/errors"
	"k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

const (
	resourceIngress = "ingresses"
	resourceSecret  = "secrets"
)

// An IngressStore is a cache of ingress resources.
type IngressStore interface {
	// Get an ingress by namespace and name. Returns an error if the ingress
	// does not exist.
	Get(namespace, name string) (*v1beta1.Ingress, error)
}

// An IngressWatch is a cache of ingress resources that notifies registered
// handlers when its contents change.
type IngressWatch struct {
	cache.SharedInformer
}

// NewIngressWatch creates a watch on ingress resources. Ingresses are cached
// and the provided ResourceEventHandlers are called when the cache changes.
func NewIngressWatch(client kubernetes.Interface, rs ...cache.ResourceEventHandler) *IngressWatch {
	lw := cache.NewListWatchFromClient(client.ExtensionsV1beta1().RESTClient(), resourceIngress, v1.NamespaceAll, fields.Everything())
	i := cache.NewSharedInformer(lw, &v1beta1.Ingress{}, 30*time.Minute)
	for _, r := range rs {
		i.AddEventHandler(r)
	}
	return &IngressWatch{i}
}

// Get an ingress by namespace and name. Returns an error if the ingress does
// not exist.
func (w *IngressWatch) Get(namespace, name string) (*v1beta1.Ingress, error) {
	key := fmt.Sprintf("%s/%s", namespace, name)
	o, exists, err := w.GetStore().GetByKey(key)
	if err != nil {
		return nil, errors.Wrapf(err, "cannot get ingress %v", key)
	}
	if !exists {
		return nil, errors.New("ingress does not exist")
	}
	return o.(*v1beta1.Ingress), nil
}

// A SecretStore is a cache of secret resources.
type SecretStore interface {
	// Get an secret by namespace and name. Returns an error if the secret does
	// not exist.
	Get(namespace, name string) (*v1.Secret, error)
}

// A SecretWatch is a cache of ingress resources that notifies registered
// handlers when its contents change.
type SecretWatch struct {
	cache.SharedInformer
}

// NewSecretWatch creates a watch on secret resources. Secrets are cached
// and the provided ResourceEventHandlers are called when the cache changes.
func NewSecretWatch(client kubernetes.Interface, rs ...cache.ResourceEventHandler) *SecretWatch {
	lw := cache.NewListWatchFromClient(client.CoreV1().RESTClient(), resourceSecret, v1.NamespaceAll, fields.Everything())
	i := cache.NewSharedInformer(lw, &v1.Secret{}, 30*time.Minute)
	for _, r := range rs {
		i.AddEventHandler(r)
	}
	return &SecretWatch{i}
}

// Get an secret by namespace and name. Returns an error if the secret does
// not exist.
func (w *SecretWatch) Get(namespace, name string) (*v1.Secret, error) {
	key := fmt.Sprintf("%s/%s", namespace, name)
	o, exists, err := w.GetStore().GetByKey(key)
	if err != nil {
		return nil, errors.Wrapf(err, "cannot get secret %v", key)
	}
	if !exists {
		return nil, errors.New("secret does not exist")
	}
	return o.(*v1.Secret), nil
}

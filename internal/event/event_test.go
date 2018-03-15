package event

import (
	"errors"
	"fmt"
	"testing"

	"k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
)

const (
	namespace       = "ns"
	coolIngressName = "coolIngress"
	coolSecretName  = "coolSecret"
)

var coolIngress = &v1beta1.Ingress{
	ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: coolIngressName},
	Spec:       v1beta1.IngressSpec{TLS: []v1beta1.IngressTLS{{SecretName: coolSecretName}}},
}

type metadata struct {
	Namespace string
	Name      string
}

type mapIngressStore map[metadata]*v1beta1.Ingress

func (m mapIngressStore) Get(namespace, name string) (*v1beta1.Ingress, error) {
	md := metadata{Namespace: namespace, Name: name}
	s, ok := m[md]
	if !ok {
		return nil, errors.New("no such ingress")
	}
	return s, nil
}

type event struct {
	md  metadata
	t   string
	r   string
	msg string
}

type mapRecorder struct {
	record.EventRecorder
	e map[event]bool
}

func (r *mapRecorder) Eventf(o runtime.Object, eventType, reason, format string, args ...interface{}) {
	i := o.(*v1beta1.Ingress)
	r.e[event{
		metadata{i.GetNamespace(), i.GetName()},
		eventType,
		reason,
		fmt.Sprintf(format, args...),
	}] = true
}

func TestNewWrite(t *testing.T) {
	cases := []struct {
		name        string
		i           mapIngressStore
		ns          string
		ingressName string
		secretName  string
		want        map[event]bool
	}{
		{
			name:        "Success",
			i:           mapIngressStore{metadata{coolIngress.GetNamespace(), coolIngress.GetName()}: coolIngress},
			ns:          namespace,
			ingressName: coolIngressName,
			secretName:  coolSecretName,
			want: map[event]bool{
				{
					metadata{namespace, coolIngressName},
					v1.EventTypeNormal,
					eventCertPairWritten,
					"Loaded TLS certificate from secret " + coolSecretName,
				}: true,
			},
		},
		{
			name:        "IngressNotInStore",
			i:           mapIngressStore{},
			ns:          namespace,
			ingressName: coolIngressName,
			secretName:  coolSecretName,
			want:        map[event]bool{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mr := &mapRecorder{e: make(map[event]bool)}
			r := NewKubernetesRecorder(mr, tc.i)
			r.NewWrite(tc.ns, tc.ingressName, tc.secretName)

			for e := range tc.want {
				if !mr.e[e] {
					t.Errorf("n.NewWrite(%v, %v, %v): want event %#v", tc.ns, tc.ingressName, tc.secretName, e)
				}
			}
			for e := range mr.e {
				if !tc.want[e] {
					t.Errorf("n.NewWrite(%v, %v, %v): got unwanted event %#v", tc.ns, tc.ingressName, tc.secretName, e)
				}
			}
		})
	}
}
func TestNewDelete(t *testing.T) {
	cases := []struct {
		name        string
		i           mapIngressStore
		ns          string
		ingressName string
		secretName  string
		want        map[event]bool
	}{
		{
			name:        "Success",
			i:           mapIngressStore{metadata{coolIngress.GetNamespace(), coolIngress.GetName()}: coolIngress},
			ns:          namespace,
			ingressName: coolIngressName,
			secretName:  coolSecretName,
			want: map[event]bool{
				{
					metadata{namespace, coolIngressName},
					v1.EventTypeNormal,
					eventCertPairDeleted,
					"Unloaded TLS certificate from secret " + coolSecretName,
				}: true,
			},
		},
		{
			name:        "IngressNotInStore",
			i:           mapIngressStore{},
			ns:          namespace,
			ingressName: coolIngressName,
			secretName:  coolSecretName,
			want:        map[event]bool{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mr := &mapRecorder{e: make(map[event]bool)}
			r := NewKubernetesRecorder(mr, tc.i)
			r.NewDelete(tc.ns, tc.ingressName, tc.secretName)

			for e := range tc.want {
				if !mr.e[e] {
					t.Errorf("n.NewDelete(%v, %v, %v): want event %#v", tc.ns, tc.ingressName, tc.secretName, e)
				}
			}
			for e := range mr.e {
				if !tc.want[e] {
					t.Errorf("n.NewDelete(%v, %v, %v): got unwanted event %#v", tc.ns, tc.ingressName, tc.secretName, e)
				}
			}
		})
	}
}

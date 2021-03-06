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
	"hash/fnv"
	"io"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/planetlabs/hal5d/internal/event"
	"github.com/planetlabs/hal5d/internal/kubernetes"
	"github.com/planetlabs/hal5d/internal/metrics"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/afero"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
)

// Labels used by metrics and logs.
const (
	LabelNamespace   = "namespace"
	LabelIngressName = "ingress_name"
	LabelSecretName  = "secret_name"
	LabelContext     = "context"
	LabelAllowHTTP   = "allow_http"
)

// Error contexts used as metric labels.
const (
	ContextUpsertIngress = "upsert_ingress"
	ContextUpsertSecret  = "upsert_secret"
	ContextDeleteIngress = "delete_ingress"
	ContextDeleteSecret  = "delete_secret"
)

const (
	certPairSuffix    = ".pem"
	certPairSeparator = "-"
	certPairMode      = 0600
)

const (
	// Corresponds to GCE Ingress annotation that accomplishes the same thing.
	// https://cloud.google.com/kubernetes-engine/docs/concepts/ingress#disabling_http
	annoAllowHTTP = "kubernetes.io/ingress.allow-http"
)

type errInvalid struct {
	error
}

// ErrInvalid wraps an error such that it will fulfill IsInvalid.
func ErrInvalid(err error) error {
	return &errInvalid{err}
}

// Invalid signals that this error indicates something was full.
func (e *errInvalid) Invalid() {}

// IsInvalid determines whether an error indicates a certificate was invalid.
// It does this by walking down the stack of errors built by pkg/errors and
// returning true for the first error that implements the following interface:
//
// type invalider interface {
//   Invalid()
// }
func IsInvalid(err error) bool {
	for {
		if _, ok := err.(interface {
			Invalid()
		}); ok {
			return true
		}
		if c, ok := err.(interface {
			Cause() error
		}); ok {
			err = c.Cause()
			continue
		}
		return false
	}
}

// collectHosts returns the host names contained within the rules of an ingress resource.
func collectHosts(i *v1beta1.Ingress) []string {
	hosts := []string(nil)
	for _, r := range i.Spec.Rules {
		if r.Host != "" {
			hosts = append(hosts, r.Host)
		}
	}
	return hosts
}

type allowHTTP string

// IsTrue indicates whether the value of the allow-http annotation is true.
func (s allowHTTP) IsTrue() bool {
	// This is quite permissive - any value other than "false" is considered true. Effectively,
	// this also means that the empty default of "" is true.
	return strings.ToLower(strings.TrimSpace(string(s))) != "false"
}

type certPair struct {
	Namespace   string
	IngressName string
	SecretName  string
}

func newCertPair(filename string) (certPair, error) {
	if !strings.HasSuffix(filename, certPairSuffix) {
		return certPair{}, errors.Errorf("filename %s does not end with expected suffix %s", filename, certPairSuffix)
	}
	parts := strings.Split(strings.TrimSuffix(filename, certPairSuffix), certPairSeparator)
	if len(parts) != 3 {
		return certPair{}, errors.Errorf("filename %s does not match expected namespace-ingressname-secretname.pem pattern", filename)
	}
	return certPair{Namespace: parts[0], IngressName: parts[1], SecretName: parts[2]}, nil
}

func (c certPair) Filename() string {
	return fmt.Sprintf("%s-%s-%s.pem", c.Namespace, c.IngressName, c.SecretName)
}

type certData struct {
	certPair
	Cert []byte
	Key  []byte
}

func (c certData) Bytes() []byte {
	return bytes.Join([][]byte{c.Cert, c.Key}, []byte("\n"))
}

type forceHTTPSMetadata struct {
	Hosts      []string
	ForceHTTPS bool
}
type forceHTTPSTable map[metadata]forceHTTPSMetadata

// Bytes returns a line-delimited encoded list of hostnames for which https should be forced.
func (da forceHTTPSTable) Bytes() []byte {
	forcedHosts := []string{}
	for _, m := range da {
		if m.ForceHTTPS {
			forcedHosts = append(forcedHosts, m.Hosts...)
		}
	}
	return []byte(strings.Join(forcedHosts, "\n"))
}

func (da forceHTTPSTable) Delete(namespace, ingressName string) {
	m := metadata{Namespace: namespace, Name: ingressName}
	delete(da, m)
}

// MarkForceHTTPS marks an ingress as HTTPS only and returns whether the setting for that ingress changed.
func (da forceHTTPSTable) MarkForceHTTPS(namespace, ingressName string, force bool, hosts []string) bool {
	changed := false

	m := metadata{Namespace: namespace, Name: ingressName}
	a := forceHTTPSMetadata{
		Hosts:      hosts,
		ForceHTTPS: force,
	}

	if existing := da[m]; !reflect.DeepEqual(existing, a) {
		changed = true
	}

	da[m] = a
	return changed
}

type metadata struct {
	Namespace string
	Name      string
}

type secretRefs map[metadata]map[string]bool

func (r secretRefs) Add(namespace, ingressName, secretName string) {
	m := metadata{Namespace: namespace, Name: secretName}
	if _, ok := r[m]; !ok {
		r[m] = make(map[string]bool)
	}
	r[m][ingressName] = true
}

func (r secretRefs) Delete(namespace, ingressName, secretName string) {
	m := metadata{Namespace: namespace, Name: secretName}
	delete(r[m], ingressName)
}

func (r secretRefs) Get(namespace, secretName string) map[string]bool {
	m := metadata{Namespace: namespace, Name: secretName}
	return r[m]
}

// A Validator determines whether cert pairs are valid.
type Validator interface {
	Validate() error
}

type optimisticValidator struct{}

func (v *optimisticValidator) Validate() error {
	return nil
}

// A Subscriber is notified synchronously every time the cert pairs change.
type Subscriber interface {
	// Changed is called every time the managed certificates change.
	Changed()
}

// Metrics that may be exposed by a certificate manager.
type Metrics struct {
	Writes   metrics.CounterVec
	Deletes  metrics.CounterVec
	Errors   metrics.CounterVec
	Invalids metrics.CounterVec
}

func newNopMetrics() Metrics {
	return Metrics{
		Writes:   &metrics.NopCounterVec{},
		Deletes:  &metrics.NopCounterVec{},
		Errors:   &metrics.NopCounterVec{},
		Invalids: &metrics.NopCounterVec{},
	}
}

// A Manager persists ingress TLS cert pairs to disk. Manager implements
// cache.ResourceEventHandler in order to consume notifications about
type Manager struct {
	log      *zap.Logger
	metric   Metrics
	recorder event.Recorder
	fs       afero.Fs

	tlsDir              string
	forceHTTPSHostsFile string
	v                   Validator
	secretStore         kubernetes.SecretStore
	secretRefs          secretRefs
	forceHTTPSTable     forceHTTPSTable
	subscribers         []Subscriber
}

// A ManagerOption can be used to configure new certificate managers.
type ManagerOption func(*Manager) error

// WithLogger configures a certificate manager's logger.
func WithLogger(l *zap.Logger) ManagerOption {
	return func(m *Manager) error {
		m.log = l
		return nil
	}
}

// WithMetrics configures a certificate manager's metrics.
func WithMetrics(mx Metrics) ManagerOption {
	return func(m *Manager) error {
		m.metric = mx
		return nil
	}
}

// WithFilesystem configures a certificate manager's filesystem implementation.
func WithFilesystem(fs afero.Fs) ManagerOption {
	return func(m *Manager) error {
		m.fs = fs
		return nil
	}
}

// WithValidator configures a certificate manager's validator. The validator
// will be called to test any new cert pairs before they are committed.
func WithValidator(v Validator) ManagerOption {
	return func(m *Manager) error {
		m.v = v
		return nil
	}
}

// WithSubscriber registers a subscriber to a certificate manager. Each
// subscriber will be called every time the managed cert pairs change.
func WithSubscriber(s Subscriber) ManagerOption {
	return func(m *Manager) error {
		m.subscribers = append(m.subscribers, s)
		return nil
	}
}

// WithEventRecorder configures a certificate manager's Kubernetes event
// recorder. The event recorder will emit events when certificate pairs change.
func WithEventRecorder(r event.Recorder) ManagerOption {
	return func(m *Manager) error {
		m.recorder = r
		return nil
	}
}

// WithForceHTTPSHostsFile specifies the location to the file hal5d will manage
// containing hostnames that should be denied http traffic.
func WithForceHTTPSHostsFile(forceHTTPSHostsFile string) ManagerOption {
	return func(m *Manager) error {
		m.forceHTTPSHostsFile = forceHTTPSHostsFile
		return nil
	}
}

// NewManager creates a new certificate manager.
func NewManager(dir string, s kubernetes.SecretStore, o ...ManagerOption) (*Manager, error) {
	m := &Manager{
		log:             zap.NewNop(),
		metric:          newNopMetrics(),
		fs:              afero.NewOsFs(),
		recorder:        &event.NopRecorder{},
		tlsDir:          dir,
		v:               &optimisticValidator{},
		secretStore:     s,
		secretRefs:      make(map[metadata]map[string]bool),
		subscribers:     make([]Subscriber, 0),
		forceHTTPSTable: forceHTTPSTable{},
	}
	for _, mo := range o {
		if err := mo(m); err != nil {
			return nil, errors.Wrap(err, "cannot apply manager option")
		}
	}
	return m, nil
}

// OnAdd handles notifications of new ingress or secret resources.
func (m *Manager) OnAdd(obj interface{}) {
	switch obj := obj.(type) {
	case *v1beta1.Ingress:
		if changed := m.upsertIngress(obj); changed {
			m.notifySubscribers()
		}
	case *v1.Secret:
		if changed := m.upsertSecret(obj); changed {
			m.notifySubscribers()
		}
	}
}

// OnUpdate handles notifications of updated ingress or secret resources.
func (m *Manager) OnUpdate(_, newObj interface{}) {
	m.OnAdd(newObj)
}

// OnDelete handles notifications of deleted ingress or secret resources.
func (m *Manager) OnDelete(obj interface{}) {
	switch obj := obj.(type) {
	case *v1beta1.Ingress:
		if changed := m.deleteIngress(obj); changed {
			m.notifySubscribers()
		}
	case *v1.Secret:
		if changed := m.deleteSecret(obj); changed {
			m.notifySubscribers()
		}
	}
}

func (m *Manager) upsertIngress(i *v1beta1.Ingress) bool { // nolint:gocyclo
	log := m.log.With(
		zap.String(LabelNamespace, i.GetNamespace()),
		zap.String(LabelIngressName, i.GetName()))
	log.Debug("processing ingress upsert")

	changed := false

	// We determine whether we should force https based on whether the `allow-http` annotation is false.
	allowHTTP := allowHTTP(i.GetAnnotations()[annoAllowHTTP]).IsTrue()
	hosts := collectHosts(i)
	if m.forceHTTPSTable.MarkForceHTTPS(i.GetNamespace(), i.GetName(), !allowHTTP, hosts) {
		changed = true
		log.With(zap.Bool(LabelAllowHTTP, allowHTTP)).Debug("configuration change for allowed http endpoints")
		if err := m.writeForceHTTPSHosts(); err != nil {
			log.Error("failed to write updated force https host list", zap.Error(err))
			m.metric.Errors.With(prometheus.Labels{LabelContext: ContextUpsertIngress}).Inc()
		}
	}

	existing, err := m.existing(i.GetNamespace(), i.GetName())
	if err != nil {
		log.Error("cannot get existing cert pairs - stale cert pairs will not be reaped")
		m.metric.Errors.With(prometheus.Labels{LabelContext: ContextUpsertIngress}).Inc()
	}

	keep := make(map[certPair]bool)
	for _, tls := range i.Spec.TLS {
		log := log.With(zap.String(LabelSecretName, tls.SecretName)) //nolint:vetshadow
		m.secretRefs.Add(i.GetNamespace(), i.GetName(), tls.SecretName)
		s, err := m.secretStore.Get(i.GetNamespace(), tls.SecretName)
		if err != nil {
			// This error is indicative of user misconfiguration, i.e. an
			// ingress referencing a TLS secret that does not yet exist. We log
			// it informationally, and do not emit an error metric.
			log.Info("cannot get TLS secret", zap.Error(err))
			m.recorder.NewInvalidSecret(i.GetNamespace(), i.GetName(), tls.SecretName)
			m.metric.Invalids.With(prometheus.Labels{
				LabelNamespace:   i.GetNamespace(),
				LabelIngressName: i.GetName(),
				LabelSecretName:  tls.SecretName,
			}).Inc()
			continue
		}
		log.Debug("found secret")

		cert, ok := s.Data[v1.TLSCertKey]
		if !ok {
			log.Info("missing certificate", zap.String("secret key", v1.TLSCertKey))
			m.recorder.NewInvalidSecret(i.GetNamespace(), i.GetName(), s.GetName())
			m.metric.Invalids.With(prometheus.Labels{
				LabelNamespace:   i.GetNamespace(),
				LabelIngressName: i.GetName(),
				LabelSecretName:  s.GetName(),
			}).Inc()
			continue
		}
		key, ok := s.Data[v1.TLSPrivateKeyKey]
		if !ok {
			log.Info("missing private key", zap.String("secret key", v1.TLSPrivateKeyKey))
			m.recorder.NewInvalidSecret(i.GetNamespace(), i.GetName(), s.GetName())
			m.metric.Invalids.With(prometheus.Labels{
				LabelNamespace:   i.GetNamespace(),
				LabelIngressName: i.GetName(),
				LabelSecretName:  s.GetName(),
			}).Inc()
			continue
		}

		cp := certPair{Namespace: i.GetNamespace(), IngressName: i.GetName(), SecretName: s.GetName()}
		cd := certData{certPair: cp, Cert: cert, Key: key}
		if existing[cp] && !m.changed(cd) {
			log.Debug("cert pair unchanged")
			keep[cp] = true
			continue
		}
		if err := m.write(cd); err != nil {
			if IsInvalid(err) {
				log.Info("invalid cert pair", zap.Error(err))
				m.recorder.NewInvalidSecret(i.GetNamespace(), i.GetName(), s.GetName())
				m.metric.Invalids.With(prometheus.Labels{
					LabelNamespace:   i.GetNamespace(),
					LabelIngressName: i.GetName(),
					LabelSecretName:  s.GetName(),
				}).Inc()
				continue
			}
			log.Error("cannot write cert pair", zap.Error(err))
			m.metric.Errors.With(prometheus.Labels{LabelContext: ContextUpsertIngress}).Inc()
			continue
		}
		keep[cp] = true
		changed = true
		m.metric.Writes.With(prometheus.Labels{
			LabelNamespace:   i.GetNamespace(),
			LabelIngressName: i.GetName(),
			LabelSecretName:  s.GetName(),
		}).Inc()
		m.recorder.NewWrite(i.GetNamespace(), i.GetName(), s.GetName())
		log.Debug("wrote cert pair")
	}

	for cp := range existing {
		if keep[cp] {
			continue
		}
		log := log.With(zap.String(LabelSecretName, cp.SecretName)) //nolint:vetshadow
		log.Debug("deleting stale cert pair")
		path := filepath.Join(m.tlsDir, cp.Filename())
		if err := m.fs.Remove(path); err != nil {
			log.Error("cannot remove stale cert pair", zap.Error(err))
			m.metric.Errors.With(prometheus.Labels{LabelContext: ContextUpsertIngress}).Inc()
			continue
		}
		m.secretRefs.Delete(i.GetNamespace(), i.GetName(), cp.SecretName)
		changed = true
		m.metric.Deletes.With(prometheus.Labels{
			LabelNamespace:   i.GetNamespace(),
			LabelIngressName: i.GetName(),
			LabelSecretName:  cp.SecretName,
		}).Inc()
		m.recorder.NewDelete(i.GetNamespace(), i.GetName(), cp.SecretName)
		log.Debug("deleted cert pair")
	}

	return changed
}

func (m *Manager) writeForceHTTPSHosts() error {
	if m.forceHTTPSHostsFile == "" {
		m.log.Debug("no force https hosts file specified, skipping")
		return nil
	}

	f, err := afero.TempFile(m.fs, filepath.Dir(m.forceHTTPSHostsFile), "https-only-tempfile")
	if err != nil {
		return err
	}
	defer f.Close()
	defer m.fs.Remove(f.Name())

	if _, err := f.Write(m.forceHTTPSTable.Bytes()); err != nil {
		return errors.Wrapf(err, "cannot write %v", f.Name())
	}

	if err := f.Sync(); err != nil {
		return errors.Wrapf(err, "cannot fsync %v", f.Name())
	}

	return errors.Wrapf(m.fs.Rename(f.Name(), m.forceHTTPSHostsFile), "cannot move %v to %v", f.Name(), m.forceHTTPSHostsFile)
}

func (m *Manager) changed(c certData) bool {
	f, err := m.fs.Open(filepath.Join(m.tlsDir, c.Filename()))
	if err != nil {
		return true
	}
	defer f.Close()

	existing := fnv.New32a()
	if _, err := io.Copy(existing, f); err != nil {
		return true
	}

	proposed := fnv.New32a()
	if _, err := proposed.Write(c.Bytes()); err != nil {
		return true
	}

	return proposed.Sum32() != existing.Sum32()
}

func (m *Manager) write(c certData) error {
	f, err := afero.TempFile(m.fs, m.tlsDir, c.Filename())
	if err != nil {
		return errors.Wrapf(err, "cannot create temp file in %v", m.tlsDir)
	}
	defer f.Close()
	defer m.fs.Remove(f.Name())

	if _, err := f.Write(c.Bytes()); err != nil {
		return errors.Wrapf(err, "cannot write cert pair data to %v", f.Name())
	}
	if err := f.Sync(); err != nil {
		return errors.Wrapf(err, "cannot fsync %v", f.Name())
	}
	if err := f.Close(); err != nil {
		return errors.Wrapf(err, "cannot close %v", f.Name())
	}
	if err := m.fs.Chmod(f.Name(), certPairMode); err != nil {
		return errors.Wrapf(err, "cannot chmod %v to %d", f.Name(), certPairMode)
	}
	// This assumes the validate function treats the temp file as it would any
	// other file in the TLS directory.
	if err := m.v.Validate(); err != nil {
		return ErrInvalid(errors.Wrapf(err, "writing certificate pair would result in invalid configuration"))
	}
	path := filepath.Join(m.tlsDir, c.Filename())
	return errors.Wrapf(m.fs.Rename(f.Name(), path), "cannot move %v to %v", f.Name(), path)
}

func (m *Manager) upsertSecret(s *v1.Secret) bool {
	log := m.log.With(
		zap.String(LabelNamespace, s.GetNamespace()),
		zap.String(LabelSecretName, s.GetName()))
	log.Debug("processing secret upsert")

	changed := false
	for ingressName := range m.secretRefs.Get(s.GetNamespace(), s.GetName()) {
		log := log.With(zap.String(LabelIngressName, ingressName)) // nolint:vetshadow
		cert, ok := s.Data[v1.TLSCertKey]
		if !ok {
			m.log.Info("missing TLS certificate", zap.String("secret key", v1.TLSCertKey))
			m.recorder.NewInvalidSecret(s.GetNamespace(), ingressName, s.GetName())
			m.metric.Invalids.With(prometheus.Labels{
				LabelNamespace:   s.GetNamespace(),
				LabelIngressName: ingressName,
				LabelSecretName:  s.GetName(),
			}).Inc()
			continue
		}
		key, ok := s.Data[v1.TLSPrivateKeyKey]
		if !ok {
			m.log.Info("missing TLS private key", zap.String("secret key", v1.TLSPrivateKeyKey))
			m.recorder.NewInvalidSecret(s.GetNamespace(), ingressName, s.GetName())
			m.metric.Invalids.With(prometheus.Labels{
				LabelNamespace:   s.GetNamespace(),
				LabelIngressName: ingressName,
				LabelSecretName:  s.GetName(),
			}).Inc()
			continue
		}

		cp := certPair{Namespace: s.GetNamespace(), IngressName: ingressName, SecretName: s.GetName()}
		cd := certData{certPair: cp, Cert: cert, Key: key}
		if !m.changed(cd) {
			log.Debug("cert pair unchanged")
			continue
		}
		if err := m.write(cd); err != nil {
			if IsInvalid(err) {
				log.Info("invalid cert pair", zap.Error(err))
				m.recorder.NewInvalidSecret(s.GetNamespace(), ingressName, s.GetName())
				m.metric.Invalids.With(prometheus.Labels{
					LabelNamespace:   s.GetNamespace(),
					LabelIngressName: ingressName,
					LabelSecretName:  s.GetName(),
				}).Inc()
				continue
			}
			log.Error("cannot write cert pair", zap.Error(err))
			m.metric.Errors.With(prometheus.Labels{LabelContext: ContextUpsertSecret}).Inc()
			continue
		}
		changed = true
		m.metric.Writes.With(prometheus.Labels{
			LabelNamespace:   s.GetNamespace(),
			LabelIngressName: ingressName,
			LabelSecretName:  s.GetName(),
		}).Inc()
		m.recorder.NewWrite(s.GetNamespace(), ingressName, s.GetName())
		log.Debug("wrote cert pair")
	}

	return changed
}

func (m *Manager) deleteIngress(i *v1beta1.Ingress) bool {
	log := m.log.With(
		zap.String(LabelNamespace, i.GetNamespace()),
		zap.String(LabelIngressName, i.GetName()))
	log.Debug("processing ingress delete")

	m.forceHTTPSTable.Delete(i.GetNamespace(), i.GetName())

	changed := false
	existing, err := m.existing(i.GetNamespace(), i.GetName())
	if err != nil {
		log.Error("cannot get existing cert pairs - stale cert pairs will not be reaped")
	}
	for cp := range existing {
		log := log.With(zap.String(LabelSecretName, cp.SecretName)) //nolint:vetshadow
		path := filepath.Join(m.tlsDir, cp.Filename())
		if err := m.fs.Remove(path); err != nil {
			log.Error("cannot remove stale cert pair", zap.Error(err))
			m.metric.Errors.With(prometheus.Labels{LabelContext: ContextDeleteIngress}).Inc()
			continue
		}
		m.secretRefs.Delete(i.GetNamespace(), i.GetName(), cp.SecretName)
		changed = true
		m.metric.Deletes.With(prometheus.Labels{
			LabelNamespace:   i.GetNamespace(),
			LabelIngressName: i.GetName(),
			LabelSecretName:  cp.SecretName,
		}).Inc()
		log.Debug("deleted cert pair")
	}

	return changed
}

func (m *Manager) deleteSecret(s *v1.Secret) bool {
	log := m.log.With(
		zap.String(LabelNamespace, s.GetNamespace()),
		zap.String(LabelSecretName, s.GetName()))
	log.Debug("processing secret delete")

	changed := false
	for ingressName := range m.secretRefs.Get(s.GetNamespace(), s.GetName()) {
		cp := certPair{Namespace: s.GetNamespace(), IngressName: ingressName, SecretName: s.GetName()}
		log := log.With(zap.String(LabelIngressName, cp.IngressName)) //nolint:vetshadow
		path := filepath.Join(m.tlsDir, cp.Filename())
		if err := m.fs.Remove(path); err != nil {
			log.Error("cannot remove stale TLS certpair", zap.Error(err))
			m.metric.Errors.With(prometheus.Labels{LabelContext: ContextDeleteSecret}).Inc()
			continue
		}
		changed = true
		m.recorder.NewDelete(s.GetNamespace(), cp.IngressName, s.GetName())
		log.Debug("deleted cert pair")
		m.metric.Deletes.With(prometheus.Labels{
			LabelNamespace:   s.GetNamespace(),
			LabelIngressName: ingressName,
			LabelSecretName:  s.GetName(),
		}).Inc()
	}

	return changed
}

func (m *Manager) existing(namespace, ingressName string) (map[certPair]bool, error) {
	fi, err := afero.ReadDir(m.fs, m.tlsDir)
	if err != nil {
		return nil, errors.Wrap(err, "cannot list TLS cert pairs")
	}

	pairs := make(map[certPair]bool)
	for _, f := range fi {
		c, err := newCertPair(f.Name())
		if err != nil {
			m.log.Debug("unexpected file in TLS dir",
				zap.String("filename", f.Name()),
				zap.String("tlsDir", m.tlsDir))
			continue
		}
		if c.Namespace != namespace {
			continue
		}
		if c.IngressName != ingressName {
			continue
		}
		pairs[c] = true
	}
	return pairs, nil
}

func (m *Manager) notifySubscribers() {
	for _, s := range m.subscribers {
		s.Changed()
	}
}

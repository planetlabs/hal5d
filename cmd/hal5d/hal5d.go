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

package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/afero"
	"go.uber.org/zap"
	"gopkg.in/alecthomas/kingpin.v2"
	client "k8s.io/client-go/kubernetes"

	"github.com/planetlabs/hal5d/internal/cert"
	"github.com/planetlabs/hal5d/internal/event"
	"github.com/planetlabs/hal5d/internal/kubernetes"
	"github.com/planetlabs/hal5d/internal/webhook"
	"github.com/planetlabs/hal5d/internal/webhook/subscriber"
	"github.com/planetlabs/hal5d/internal/webhook/validator"
)

// https://github.com/tuenti/haproxy-docker-wrapper defaults.
const (
	defaultWebhookURLValidate = "http://localhost:15000/validate"
	defaultWebhookURLReload   = "http://localhost:15000/reload"
)

const (
	prometheusNamespace = "hal5d"
	syncEventBuffer     = 128
)

func main() {
	var (
		app                 = kingpin.New(filepath.Base(os.Args[0]), "Manages an haproxy frontend for linkerd's Kubernetes ingress controller.").DefaultEnvars()
		debug               = app.Flag("debug", "Run with debug logging.").Short('d').Bool()
		dir                 = app.Flag("tls-dir", "Directory in which TLS certificates are managed.").Default("/tls").String()
		forceHTTPSHostsFile = app.Flag("force-https-hosts-file", "File in which the forced https host list is managed.").Default("").String()
		kubecfg             = app.Flag("kubeconfig", "Path to kubeconfig file. Leave unset to use in-cluster config.").String()
		apiserver           = app.Flag("master", "Address of Kubernetes API server. Leave unset to use in-cluster config.").String()
		vURL                = app.Flag("validate-url", "Webhook URL used to validate haproxy configuration.").Default(defaultWebhookURLValidate).String()
		rURL                = app.Flag("reload-url", "Webhook URL used to reload haproxy configuration.").Default(defaultWebhookURLReload).String()
		listen              = app.Flag("listen", "Address at which to expose /metrics and /healthz.").Default(":10002").String()
	)
	kingpin.MustParse(app.Parse(os.Args[1:]))
	glogWorkaround()

	var (
		writes = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: prometheusNamespace,
				Name:      "certpair_writes_total",
				Help:      "Total certificate pairs written to disk.",
			},
			[]string{cert.LabelNamespace, cert.LabelIngressName, cert.LabelSecretName},
		)
		deletes = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: prometheusNamespace,
				Name:      "certpair_deletes_total",
				Help:      "Total certificate pairs deleted from disk.",
			},
			[]string{cert.LabelNamespace, cert.LabelIngressName, cert.LabelSecretName},
		)
		errors = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: prometheusNamespace,
				Name:      "errors_total",
				Help:      "Total errors encountered while managing certificate pairs.",
			},
			[]string{cert.LabelContext},
		)
		invalids = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: prometheusNamespace,
				Name:      "invalids_total",
				Help:      "Total invalid secrets encountered while managing certificate pairs.",
			},
			[]string{cert.LabelNamespace, cert.LabelIngressName, cert.LabelSecretName},
		)
	)
	prometheus.MustRegister(writes, deletes, errors, invalids)

	log, err := zap.NewProduction()
	if *debug {
		log, err = zap.NewDevelopment()
	}
	kingpin.FatalIfError(err, "cannot create log")
	defer log.Sync()

	mx := cert.Metrics{Writes: writes, Deletes: deletes, Errors: errors, Invalids: invalids}

	c, err := kubernetes.BuildConfigFromFlags(*apiserver, *kubecfg)
	kingpin.FatalIfError(err, "cannot create Kubernetes client configuration")

	cs, err := client.NewForConfig(c)
	kingpin.FatalIfError(err, "cannot create Kubernetes client")

	ingresses := kubernetes.NewIngressWatch(cs)
	secrets := kubernetes.NewSecretWatch(cs)
	e := kubernetes.NewEventRecorder(cs)

	v := validator.New(webhook.New(*vURL))
	s, err := subscriber.New(webhook.New(*rURL), subscriber.WithLogger(log))
	kingpin.FatalIfError(err, "cannot create reload webhook")

	// Check for the https-only host list. If this file does not exist, and haproxy
	// is configured to use it, it will report configuration errors given the example
	// configuration we propose. In order to avoid races in kubernetes, we recommend
	// using an initContainer to create this file before either container in the pod
	// starts.
	if *forceHTTPSHostsFile != "" {
		if _, err = os.Stat(*forceHTTPSHostsFile); err != nil {
			kingpin.FatalIfError(err, "cannot open force-https-hosts file")
		}
	}

	// This works around the race when a pod running both haproxy and hal5d
	// starts. If hal5d starts first and writes out some TLS certificates fast
	// enough they will fail validation due to the haproxy container not being
	// up yet. This will result in TLS certificates not being written until the
	// watch caches are refreshed 30 minutes after the pod starts.
	for err = v.Validate(); err != nil; err = v.Validate() {
		log.Info("waiting for valid haproxy configuration", zap.Error(err))
		time.Sleep(2 * time.Second)
	}

	m, err := cert.NewManager(*dir, secrets,
		cert.WithLogger(log),
		cert.WithMetrics(mx),
		cert.WithEventRecorder(event.NewKubernetesRecorder(e, ingresses)),
		cert.WithFilesystem(afero.NewOsFs()),
		cert.WithValidator(v),
		cert.WithSubscriber(s),
		cert.WithForceHTTPSHostsFile(*forceHTTPSHostsFile),
	)
	kingpin.FatalIfError(err, "cannot create certificate manager")

	sync := kubernetes.NewSynchronousResourceEventHandler(m, syncEventBuffer)
	ingresses.AddEventHandler(sync)
	secrets.AddEventHandler(sync)

	h := &httpRunner{l: *listen, h: map[string]http.Handler{
		"/metrics": promhttp.Handler(),
		"/healthz": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.Body.Close() }), // nolint:gas,gosec
	}}

	kingpin.FatalIfError(await(h, sync, ingresses, secrets), "error watching Kubernetes")
}

type runner interface {
	Run(stop <-chan struct{})
}

func await(rs ...runner) error {
	stop := make(chan struct{})
	g := &run.Group{}
	for i := range rs {
		r := rs[i] // https://golang.org/doc/faq#closures_and_goroutines
		g.Add(func() error { r.Run(stop); return nil }, func(err error) { close(stop) })
	}
	return g.Run()
}

type httpRunner struct {
	l string
	h map[string]http.Handler
}

func (r *httpRunner) Run(stop <-chan struct{}) {
	rt := httprouter.New()
	for path, handler := range r.h {
		rt.Handler("GET", path, handler)
	}

	s := &http.Server{Addr: r.l, Handler: rt}
	ctx, cancel := context.WithTimeout(context.Background(), 0*time.Second)
	go func() {
		<-stop
		s.Shutdown(ctx) // nolint:gas,gosec
	}()
	s.ListenAndServe() // nolint:gas,gosec
	cancel()
	return
}

// Many Kubernetes client things depend on glog. glog gets sad when flag.Parse()
// is not called before it tries to emit a log line. flag.Parse() fights with
// kingpin.
func glogWorkaround() {
	os.Args = []string{os.Args[0], "-logtostderr=true", "-v=0", "-vmodule="}
	flag.Parse()
}

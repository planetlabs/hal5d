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

	"github.com/negz/hal5d/internal/cert"
	"github.com/negz/hal5d/internal/event"
	"github.com/negz/hal5d/internal/kubernetes"
	"github.com/negz/hal5d/internal/webhook"
	"github.com/negz/hal5d/internal/webhook/subscriber"
	"github.com/negz/hal5d/internal/webhook/validator"
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
		app       = kingpin.New(filepath.Base(os.Args[0]), "Manages an haproxy frontend for linkerd's Kubernetes ingress controller.").DefaultEnvars()
		debug     = app.Flag("debug", "Run with debug logging.").Short('d').Bool()
		dir       = app.Flag("tls-dir", "Directory in which TLS certificates are managed.").Default("/tls").String()
		kubecfg   = app.Flag("kubeconfig", "Path to kubeconfig file. Leave unset to use in-cluster config.").String()
		apiserver = app.Flag("master", "Address of Kubernetes API server. Leave unset to use in-cluster config.").String()
		vURL      = app.Flag("validate-url", "Webhook URL used to validate haproxy configuration.").Default(defaultWebhookURLValidate).String()
		rURL      = app.Flag("reload-url", "Webhook URL used to reload haproxy configuration.").Default(defaultWebhookURLReload).String()
		listen    = app.Flag("listen", "Address at which to expose /metrics and /healthz.").Default(":10002").String()
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
				Help:      "Total certificate pairs deletes from disk.",
			},
			[]string{cert.LabelNamespace, cert.LabelIngressName, cert.LabelSecretName},
		)
		errors = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: prometheusNamespace,
				Name:      "errors_total",
				Help:      "Total errors encountered while managing certificate pairs.",
			},
			[]string{cert.LabelErrorContext},
		)
	)
	prometheus.MustRegister(writes, deletes, errors)

	log, err := zap.NewProduction()
	if *debug {
		log, err = zap.NewDevelopment()
	}
	kingpin.FatalIfError(err, "cannot create log")
	defer log.Sync()

	mx := cert.Metrics{Writes: writes, Deletes: deletes, Errors: errors}

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

	m, err := cert.NewManager(*dir, secrets,
		cert.WithLogger(log),
		cert.WithMetrics(mx),
		cert.WithEventRecorder(event.NewKubernetesRecorder(e, ingresses)),
		cert.WithFilesystem(afero.NewOsFs()),
		cert.WithValidator(v),
		cert.WithSubscriber(s))
	kingpin.FatalIfError(err, "cannot create certificate manager")

	sync := kubernetes.NewSynchronousResourceEventHandler(m, syncEventBuffer)
	ingresses.AddEventHandler(sync)
	secrets.AddEventHandler(sync)

	h := &httpRunner{l: *listen, h: map[string]http.Handler{
		"/metrics": promhttp.Handler(),
		"/healthz": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.Body.Close() }), // nolint:gas
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
		s.Shutdown(ctx) // nolint:gas
	}()
	s.ListenAndServe() // nolint:gas
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

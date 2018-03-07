package main

import (
	"flag"
	"os"
	"path/filepath"

	"github.com/oklog/run"
	"github.com/spf13/afero"
	"go.uber.org/zap"
	"gopkg.in/alecthomas/kingpin.v2"
	client "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/negz/hal5d/internal/cert"
	"github.com/negz/hal5d/internal/kubernetes"
	"github.com/negz/hal5d/internal/webhook"
	"github.com/negz/hal5d/internal/webhook/subscriber"
	"github.com/negz/hal5d/internal/webhook/validator"
)

// https://github.com/tuenti/haproxy-docker-wrapper defaults.
const (
	defaultWebhookURLValidate = "http://localhost:15000/validate"
	defaultWebhookURLReload   = "http://localhost:15000/reload"

	syncEventBuffer = 128
)

func main() {
	var (
		app       = kingpin.New(filepath.Base(os.Args[0]), "Manages an haproxy frontend for linkerd's Kubernetes ingress controller.").DefaultEnvars()
		debug     = app.Flag("debug", "Run with debug logging.").Short('d').Bool()
		dir       = app.Flag("tls-dir", "Directory in which TLS certificates are managed.").Default("/tls").String()
		kubecfg   = app.Flag("kubeconfig", "Path to kubeconfig file. Leave unset to use in-cluster config.").String()
		apiserver = app.Flag("master", "Address of Kubernetes API server. Leave unset to use in-cluster config.").String()
		vURL      = app.Flag("validate-url", "Webhook URL used to validate haproxy configuration.").Default(defaultWebhookURLValidate).String()
		rURL      = app.Flag("reload-url", "Webhook URL used to reload haproxy configuration.").Default(defaultWebhookURLValidate).String()
	)

	kingpin.MustParse(app.Parse(os.Args[1:]))
	glogWorkaround()

	log, err := zap.NewProduction()
	if *debug {
		log, err = zap.NewDevelopment()
	}
	kingpin.FatalIfError(err, "cannot create log")

	c, err := buildConfigFromFlags(*apiserver, *kubecfg)
	kingpin.FatalIfError(err, "cannot create Kubernetes client configuration")

	cs, err := client.NewForConfig(c)
	kingpin.FatalIfError(err, "cannot create Kubernetes client")

	ingresses := kubernetes.NewIngressWatch(cs)
	secrets := kubernetes.NewSecretWatch(cs)

	v := validator.New(webhook.New(*vURL))
	s, err := subscriber.New(webhook.New(*rURL), subscriber.WithLogger(log))
	kingpin.FatalIfError(err, "cannot create reload webhook")

	m, err := cert.NewManager(*dir, secrets,
		cert.WithLogger(log),
		cert.WithFilesystem(afero.NewOsFs()),
		cert.WithValidator(v),
		cert.WithSubscriber(s))
	kingpin.FatalIfError(err, "cannot create certificate manager")

	sync := kubernetes.NewSynchronousResourceEventHandler(m, syncEventBuffer)
	ingresses.AddEventHandler(sync)
	secrets.AddEventHandler(sync)

	kingpin.FatalIfError(await(sync, ingresses, secrets), "error watching Kubernetes")
}

type runner interface {
	Run(stop <-chan struct{})
}

func await(rs ...runner) error {
	stop := make(chan struct{})
	g := &run.Group{}
	for i := range rs {
		r := rs[i] // https://github.com/golang/go/wiki/CommonMistakes#using-goroutines-on-loop-iterator-variables
		g.Add(func() error { r.Run(stop); return nil }, func(err error) { close(stop) })
	}
	return g.Run()
}

// Many Kubernetes client things depend on glog. glog gets sad when flag.Parse()
// is not called before it tries to emit a log line. flag.Parse() fights with
// kingpin.
func glogWorkaround() {
	os.Args = []string{os.Args[0], "-logtostderr=true", "-v=0", "-vmodule="}
	flag.Parse()
}

// https://godoc.org/k8s.io/client-go/tools/clientcmd#BuildConfigFromFlags with
// no annoying dependencies on glog.
func buildConfigFromFlags(apiserver, kubecfg string) (*rest.Config, error) {
	if kubecfg != "" || apiserver != "" {
		return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubecfg},
			&clientcmd.ConfigOverrides{ClusterInfo: api.Cluster{Server: apiserver}}).ClientConfig()
	}
	return rest.InClusterConfig()
}

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf"
	_ "github.com/lib/pq"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"

	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/go-mod/cfg"
	"github.com/cyverse-de/go-mod/gotelnats"
	"github.com/cyverse-de/go-mod/logging"
	"github.com/cyverse-de/go-mod/otelutils"
	"github.com/cyverse-de/go-mod/protobufjson"
	"github.com/pkg/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog" // pull in to set klog output to stderr

	"github.com/uptrace/opentelemetry-go-extra/otelsql"
	"github.com/uptrace/opentelemetry-go-extra/otelsqlx"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
)

var log = common.Log

const serviceName = "app-exposer"

func init() {
	// Set klog, used by the k8s client, to use its "log to stderr"
	// functionality. Otherwise, it'll crash without a /tmp directory, and
	// it's not great it's allowed to write files by default anyway. With
	// thanks to https://github.com/coredns/coredns/pull/2529
	klogFlags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(klogFlags)
	logtostderr := klogFlags.Lookup("logtostderr")
	logtostderr.Value.Set("true") // nolint:errcheck
}

func wrapOtelTransport(rt http.RoundTripper) http.RoundTripper {
	return otelhttp.NewTransport(rt)
}

func main() {
	log.Logger.SetReportCaller(true)

	var (
		err        error
		kubeconfig *string
		c          *koanf.Koanf
		db         *sqlx.DB

		configPath = flag.String("config", cfg.DefaultConfigPath, "Path to the config file")
		dotEnvPath = flag.String("dotenv-path", cfg.DefaultDotEnvPath, "Path to the dotenv file")
		tlsCert    = flag.String("tlscert", gotelnats.DefaultTLSCertPath, "Path to the NATS TLS cert file")
		tlsKey     = flag.String("tlskey", gotelnats.DefaultTLSKeyPath, "Path to the NATS TLS key file")
		caCert     = flag.String("tlsca", gotelnats.DefaultTLSCAPath, "Path to the NATS TLS CA file")
		credsPath  = flag.String("creds", gotelnats.DefaultCredsPath, "Path to the NATS creds file")
		//maxReconnects                 = flag.Int("max-reconnects", gotelnats.DefaultMaxReconnects, "Maximum number of reconnection attempts to NATS")
		//reconnectWait                 = flag.Int("reconnect-wait", gotelnats.DefaultReconnectWait, "Seconds to wait between reconnection attempts to NATS")
		envPrefix                     = flag.String("env-prefix", cfg.DefaultEnvPrefix, "The prefix for environment variables")
		namespace                     = flag.String("namespace", "default", "The namespace scope this process operates on for non-VICE calls")
		viceNamespace                 = flag.String("vice-namespace", "vice-apps", "The namepsace that VICE apps are launched within")
		listenPort                    = flag.Int("port", 60000, "(optional) The port to listen on")
		ingressClass                  = flag.String("ingress-class", "nginx", "(optional) the ingress class to use")
		viceProxy                     = flag.String("vice-proxy", "harbor.cyverse.org/de/vice-proxy", "The image name of the proxy to use for VICE apps. The image tag is set in the config.")
		viceDefaultBackendService     = flag.String("vice-default-backend", "vice-default-backend", "The name of the service to use as the default backend for VICE ingresses")
		viceDefaultBackendServicePort = flag.Int("vice-default-backend-port", 80, "The port for the default backend for VICE ingresses")
		getAnalysisIDService          = flag.String("get-analysis-id-service", "get-analysis-id", "The service name for the service that provides analysis ID lookups")
		checkResourceAccessService    = flag.String("check-resource-access-service", "check-resource-access", "The name of the service that validates whether a user can access a resource")
		userSuffix                    = flag.String("user-suffix", "@iplantcollaborative.org", "The user suffix for all users in the DE installation")
		logLevel                      = flag.String("log-level", "warn", "One of trace, debug, info, warn, error, fatal, or panic.")
	)

	var tracerCtx, cancel = context.WithCancel(context.Background())
	defer cancel()
	shutdown := otelutils.TracerProviderFromEnv(tracerCtx, serviceName, func(e error) { log.Fatal(e) })
	defer shutdown()

	// if cluster is set, then
	if cluster := os.Getenv("CLUSTER"); cluster != "" {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	} else {
		// If the home directory exists, then assume that the kube config will be read
		// from ~/.kube/config.
		if home := os.Getenv("HOME"); home != "" {
			kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
		} else {
			// If the home directory doesn't exist, then allow the user to specify a path.
			kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
		}
	}

	flag.Parse()
	logging.SetupLogging(*logLevel)

	nats.RegisterEncoder("protojson", protobufjson.NewCodec(protobufjson.WithEmitUnpopulated()))

	log := log.WithFields(logrus.Fields{"context": "main"})

	log.Infof("Reading config from %s", *configPath)
	if _, err = os.Open(*configPath); err != nil {
		log.Fatal(*configPath)
	}

	c, err = cfg.Init(&cfg.Settings{
		EnvPrefix:   *envPrefix,
		ConfigPath:  *configPath,
		DotEnvPath:  *dotEnvPath,
		StrictMerge: false,
		FileType:    cfg.YAML,
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Infof("Done reading config from %s", *configPath)

	// Make sure the db.uri URL is parseable
	if _, err = url.Parse(c.String("db.uri")); err != nil {
		log.Fatal(errors.Wrap(err, "Can't parse db.uri in the config file"))
	}

	// Make sure the frontend base URL is parseable.
	if _, err = url.Parse(c.String("k8s.frontend.base")); err != nil {
		log.Fatal(errors.Wrap(err, "Can't parse k8s.frontend.base in the config file"))
	}

	// Make sure that the iRODS zone isn't empty.
	zone := c.String("irods.zone")
	if zone == "" {
		log.Fatal("The iRODS zone must be specified in the config file")
	}

	natsCluster := c.String("nats.cluster")
	if natsCluster == "" {
		log.Fatalf("The %s_NATS_CLUSTER environment variable or nats.cluster configuration value must be set", *envPrefix)
	}

	// Print error and exit if *kubeconfig is not empty and doesn't actually
	// exist. If *kubeconfig is blank, then the app may be running inside the
	// cluster, so let things proceed.
	if *kubeconfig != "" {
		_, err = os.Stat(*kubeconfig)
		if err != nil {
			if os.IsNotExist(err) {
				log.Fatalf("config %s does not exist", *kubeconfig)
			}
			log.Fatal(errors.Wrapf(err, "error stat'ing the kubeconfig %s", *kubeconfig))
		}
	}

	log.Printf("namespace is set to %s\n", *namespace)
	log.Printf("listen port is set to %d\n", *listenPort)
	log.Printf("kubeconfig is set to '%s', and may be blank", *kubeconfig)

	var config *rest.Config
	if *kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
		if err != nil {
			log.Fatal(errors.Wrapf(err, "error building config from flags using kubeconfig %s", *kubeconfig))
		}
	} else {
		// If the home directory doesn't exist and the user doesn't specify a path,
		// then assume that we're running inside a cluster.
		config, err = rest.InClusterConfig()
		if err != nil {
			log.Fatal(errors.Wrapf(err, "error loading the config inside the cluster"))
		}
	}

	config.Wrap(wrapOtelTransport)

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatal(errors.Wrap(err, "error creating clientset from config"))
	}

	var proxyImage string
	proxyTag := c.String("interapps.proxy.tag")
	if proxyTag == "" {
		proxyImage = *viceProxy
	} else {
		proxyImage = fmt.Sprintf("%s:%s", *viceProxy, proxyTag)
	}

	dbURI := c.String("db.uri")
	db = otelsqlx.MustConnect("postgres", dbURI,
		otelsql.WithAttributes(semconv.DBSystemPostgreSQL))

	log.Infof("NATS TLS cert file is %s", *tlsCert)
	log.Infof("NATS TLS key file is %s", *tlsKey)
	log.Infof("NATS CA cert file is %s", *caCert)
	log.Infof("NATS creds file is %s", *credsPath)

	exposerInit := &ExposerAppInit{
		Namespace:                     *namespace,
		ViceNamespace:                 *viceNamespace,
		ViceProxyImage:                proxyImage,
		ViceDefaultBackendService:     *viceDefaultBackendService,
		ViceDefaultBackendServicePort: *viceDefaultBackendServicePort,
		GetAnalysisIDService:          *getAnalysisIDService,
		CheckResourceAccessService:    *checkResourceAccessService,
		db:                            db,
		UserSuffix:                    *userSuffix,
		IRODSZone:                     zone,
		IngressClass:                  *ingressClass,
		ClientSet:                     clientset,
		NATSCluster:                   natsCluster,
		NATSTLSKey:                    *tlsKey,
		NATSTLSCert:                   *tlsCert,
		NATSTLSCA:                     *caCert,
		NATSCredsFilePath:             *credsPath,
	}

	a := apps.NewApps(db, *userSuffix)
	go a.Run()
	defer a.Finish()
	app := NewExposerApp(
		exposerInit,
		a,
		c,
	)
	log.Printf("listening on port %d", *listenPort)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", strconv.Itoa(*listenPort)), app.router))
}

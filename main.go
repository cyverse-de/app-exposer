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
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/configurate"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog" // pull in to set klog output to stderr

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
)

var log = common.Log

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

func jaegerTracerProvider(url string) (*tracesdk.TracerProvider, error) {
	// Create the Jaeger exporter
	exp, err := jaeger.New(jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(url)))
	if err != nil {
		return nil, err
	}

	tp := tracesdk.NewTracerProvider(
		tracesdk.WithBatcher(exp),
		tracesdk.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("app-exposer"),
		)),
	)

	return tp, nil
}

func main() {
	log.Logger.SetReportCaller(true)

	var (
		err        error
		kubeconfig *string
		cfg        *viper.Viper
		db         *sqlx.DB

		configPath                    = flag.String("config", "/etc/iplant/de/jobservices.yml", "Path to the config file")
		namespace                     = flag.String("namespace", "default", "The namespace scope this process operates on for non-VICE calls")
		viceNamespace                 = flag.String("vice-namespace", "vice-apps", "The namepsace that VICE apps are launched within")
		listenPort                    = flag.Int("port", 60000, "(optional) The port to listen on")
		ingressClass                  = flag.String("ingress-class", "nginx", "(optional) the ingress class to use")
		viceProxy                     = flag.String("vice-proxy", "harbor.cyverse.org/de/vice-proxy", "The image name of the proxy to use for VICE apps. The image tag is set in the config.")
		viceDefaultBackendService     = flag.String("vice-default-backend", "vice-default-backend", "The name of the service to use as the default backend for VICE ingresses")
		viceDefaultBackendServicePort = flag.Int("vice-default-backend-port", 80, "The port for the default backend for VICE ingresses")
		getAnalysisIDService          = flag.String("--get-analysis-id-service", "get-analysis-id", "The service name for the service that provides analysis ID lookups")
		checkResourceAccessService    = flag.String("--check-resource-access-service", "check-resource-access", "The name of the service that validates whether a user can access a resource")
		userSuffix                    = flag.String("user-suffix", "@iplantcollaborative.org", "The user suffix for all users in the DE installation")
		logLevel                      = flag.String("log-level", "warn", "One of trace, debug, info, warn, error, fatal, or panic.")

		tracerProvider *tracesdk.TracerProvider
	)

	otelTracesExporter := os.Getenv("OTEL_TRACES_EXPORTER")
	if otelTracesExporter == "jaeger" {
		jaegerEndpoint := os.Getenv("OTEL_EXPORTER_JAEGER_ENDPOINT")
		if jaegerEndpoint == "" {
			log.Warn("Jaeger set as OpenTelemetry trace exporter, but no Jaeger endpoint configured.")
		} else {
			tp, err := jaegerTracerProvider(jaegerEndpoint)
			if err != nil {
				log.Fatal(err)
			}
			tracerProvider = tp
			otel.SetTracerProvider(tp)
			otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
		}
	}

	if tracerProvider != nil {
		tracerCtx, cancel := context.WithCancel(context.Background())
		defer cancel()

		defer func(tracerContext context.Context) {
			ctx, cancel := context.WithTimeout(tracerContext, time.Second*5)
			defer cancel()
			if err := tracerProvider.Shutdown(ctx); err != nil {
				log.Fatal(err)
			}
		}(tracerCtx)
	}

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

	var levelSetting logrus.Level

	switch *logLevel {
	case "trace":
		levelSetting = logrus.TraceLevel
	case "debug":
		levelSetting = logrus.DebugLevel
	case "info":
		levelSetting = logrus.InfoLevel
	case "warn":
		levelSetting = logrus.WarnLevel
	case "error":
		levelSetting = logrus.ErrorLevel
	case "fatal":
		levelSetting = logrus.FatalLevel
	case "panic":
		levelSetting = logrus.PanicLevel
	default:
		log.Fatal("incorrect log level")
	}

	log.Logger.SetLevel(levelSetting)

	log.Infof("Reading config from %s", *configPath)
	if _, err = os.Open(*configPath); err != nil {
		log.Fatal(*configPath)
	}

	cfg, err = configurate.Init(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	log.Infof("Done reading config from %s", *configPath)

	// Make sure the db.uri URL is parseable
	if _, err = url.Parse(cfg.GetString("db.uri")); err != nil {
		log.Fatal(errors.Wrap(err, "Can't parse db.uri in the config file"))
	}

	// Make sure the frontend base URL is parseable.
	if _, err = url.Parse(cfg.GetString("k8s.frontend.base")); err != nil {
		log.Fatal(errors.Wrap(err, "Can't parse k8s.frontend.base in the config file"))
	}

	// Make sure that the iRODS zone isn't empty.
	zone := cfg.GetString("irods.zone")
	if zone == "" {
		log.Fatal("The iRODS zone must be specified in the config file")
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

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatal(errors.Wrap(err, "error creating clientset from config"))
	}

	jobStatusURL := cfg.GetString("vice.job-status.base")
	if jobStatusURL == "" {
		jobStatusURL = "http://job-status-listener"
	}

	appsServiceBaseURL := cfg.GetString("apps.base")
	if appsServiceBaseURL == "" {
		appsServiceBaseURL = "http://apps"
	}

	metadataBaseURL := cfg.GetString("metadata.base")
	if metadataBaseURL == "" {
		metadataBaseURL = "http://metadata"
	}

	permissionsURL := cfg.GetString("permissions.base")
	if permissionsURL == "" {
		permissionsURL = "http://permissions"
	}

	var proxyImage string
	proxyTag := cfg.GetString("interapps.proxy.tag")
	if proxyTag == "" {
		proxyImage = *viceProxy
	} else {
		proxyImage = fmt.Sprintf("%s:%s", *viceProxy, proxyTag)
	}

	dbURI := cfg.GetString("db.uri")
	db = sqlx.MustConnect("postgres", dbURI)

	exposerInit := &ExposerAppInit{
		Namespace:                     *namespace,
		ViceNamespace:                 *viceNamespace,
		PorklockImage:                 cfg.GetString("vice.file-transfers.image"),
		PorklockTag:                   cfg.GetString("vice.file-transfers.tag"),
		UseCSIDriver:                  cfg.GetBool("vice.use_csi_driver"),
		InputPathListIdentifier:       cfg.GetString("path_list.file_identifier"),
		TicketInputPathListIdentifier: cfg.GetString("tickets_path_list.file_identifier"),
		ImagePullSecretName:           cfg.GetString("vice.image-pull-secret"),
		JobStatusURL:                  jobStatusURL,
		ViceProxyImage:                proxyImage,
		CASBaseURL:                    cfg.GetString("cas.base"),
		FrontendBaseURL:               cfg.GetString("k8s.frontend.base"),
		ViceDefaultBackendService:     *viceDefaultBackendService,
		ViceDefaultBackendServicePort: *viceDefaultBackendServicePort,
		GetAnalysisIDService:          *getAnalysisIDService,
		CheckResourceAccessService:    *checkResourceAccessService,
		VICEBackendNamespace:          cfg.GetString("vice.backend-namespace"),
		AppsServiceBaseURL:            appsServiceBaseURL,
		db:                            db,
		UserSuffix:                    *userSuffix,
		MetadataBaseURL:               metadataBaseURL,
		PermissionsURL:                permissionsURL,
		KeycloakBaseURL:               cfg.GetString("keycloak.base"),
		KeycloakRealm:                 cfg.GetString("keycloak.realm"),
		KeycloakClientID:              cfg.GetString("keycloak.client-id"),
		KeycloakClientSecret:          cfg.GetString("keycloak.client-secret"),
		IRODSZone:                     zone,
	}

	a := apps.NewApps(db, *userSuffix)
	go a.Run()
	defer a.Finish()
	app := NewExposerApp(exposerInit, *ingressClass, clientset, a)
	log.Printf("listening on port %d", *listenPort)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", strconv.Itoa(*listenPort)), app.router))
}

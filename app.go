package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/external"
	"github.com/cyverse-de/app-exposer/instantlaunches"
	"github.com/cyverse-de/app-exposer/internal"
	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
	"k8s.io/client-go/kubernetes"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// ExposerApp encapsulates the overall application-logic, tying together the
// REST-like API with the underlying Kubernetes API. All of the HTTP handlers
// are methods for an ExposerApp instance.
type ExposerApp struct {
	external        *external.External
	internal        *internal.Internal
	namespace       string
	clientset       kubernetes.Interface
	router          *echo.Echo
	db              *sqlx.DB
	instantlaunches *instantlaunches.App
}

// ExposerAppInit contains configuration settings for creating a new ExposerApp.
type ExposerAppInit struct {
	Namespace                     string // The namespace that the Ingress settings are added to.
	ViceNamespace                 string // The namespace containing the running VICE apps.
	ViceProxyImage                string
	ViceDefaultBackendService     string
	ViceDefaultBackendServicePort int
	GetAnalysisIDService          string
	CheckResourceAccessService    string
	db                            *sqlx.DB
	UserSuffix                    string
	IRODSZone                     string
	IngressClass                  string
	ClientSet                     kubernetes.Interface
	NATSCluster                   string
	NATSTLSKey                    string
	NATSTLSCert                   string
	NATSTLSCA                     string
	NATSCredsFilePath             string
	NATSMaxReconnects             int
	NATSReconnectWait             int
}

// NewExposerApp creates and returns a newly instantiated *ExposerApp.
func NewExposerApp(init *ExposerAppInit, apps *apps.Apps, c *koanf.Koanf) *ExposerApp {
	jobStatusURL := c.String("vice.job-status.base")
	if jobStatusURL == "" {
		jobStatusURL = "http://job-status-listener"
	}

	metadataBaseURL := c.String("metadata.base")
	if metadataBaseURL == "" {
		metadataBaseURL = "http://metadata"
	}

	appsServiceBaseURL := c.String("apps.base")
	if appsServiceBaseURL == "" {
		appsServiceBaseURL = "http://apps"
	}

	permissionsURL := c.String("permissions.base")
	if permissionsURL == "" {
		permissionsURL = "http://permissions"
	}

	nc, err := nats.Connect(
		init.NATSCluster,
		nats.UserCredentials(init.NATSCredsFilePath),
		nats.RootCAs(init.NATSTLSCA),
		nats.ClientCert(init.NATSTLSCert, init.NATSTLSKey),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(init.NATSMaxReconnects),
		nats.ReconnectWait(time.Duration(init.NATSReconnectWait)*time.Second),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			if err != nil {
				log.Errorf("disconnected from nats: %s", err.Error())
			} else {
				log.Error("disconnected from nats with no error")
			}
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Infof("reconnected to %s", nc.ConnectedUrl())
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			log.Errorf("connection closed: %s", nc.LastError().Error())
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	log.Infof("configured servers: %s", strings.Join(nc.Servers(), " "))
	log.Infof("connected to NATS host: %s", nc.ConnectedServerName())

	conn, err := nats.NewEncodedConn(nc, "protojson")
	if err != nil {
		log.Fatal(err)
	}

	internalInit := &internal.Init{
		ViceNamespace:                 init.ViceNamespace,
		PorklockImage:                 c.String("vice.file-transfers.image"),
		PorklockTag:                   c.String("vice.file-transfers.tag"),
		UseCSIDriver:                  c.Bool("vice.use_csi_driver"),
		InputPathListIdentifier:       c.String("path_list.file_identifier"),
		TicketInputPathListIdentifier: c.String("tickets_path_list.file_identifier"),
		ImagePullSecretName:           c.String("vice.image-pull-secret"),
		ViceProxyImage:                init.ViceProxyImage,
		FrontendBaseURL:               c.String("k8s.frontend.base"),
		ViceDefaultBackendService:     init.ViceDefaultBackendService,
		ViceDefaultBackendServicePort: init.ViceDefaultBackendServicePort,
		GetAnalysisIDService:          init.GetAnalysisIDService,
		CheckResourceAccessService:    init.CheckResourceAccessService,
		VICEBackendNamespace:          c.String("vice.backend-namespace"),
		AppsServiceBaseURL:            appsServiceBaseURL,
		JobStatusURL:                  jobStatusURL,
		UserSuffix:                    init.UserSuffix,
		PermissionsURL:                permissionsURL,
		KeycloakBaseURL:               c.String("keycloak.base"),
		KeycloakRealm:                 c.String("keycloak.realm"),
		KeycloakClientID:              c.String("keycloak.client-id"),
		KeycloakClientSecret:          c.String("keycloak.client-secret"),
		IRODSZone:                     init.IRODSZone,
		IngressClass:                  init.IngressClass,
		NATSEncodedConn:               conn,
	}

	app := &ExposerApp{
		external:  external.New(init.ClientSet, init.Namespace, init.IngressClass),
		internal:  internal.New(internalInit, init.db, init.ClientSet, apps),
		namespace: init.Namespace,
		clientset: init.ClientSet,
		router:    echo.New(),
		db:        init.db,
	}

	app.router.Use(otelecho.Middleware("app-exposer"))
	app.router.Use(middleware.Logger())

	ilInit := &instantlaunches.Init{
		UserSuffix:      init.UserSuffix,
		MetadataBaseURL: metadataBaseURL,
		PermissionsURL:  permissionsURL,
	}

	app.router.HTTPErrorHandler = func(err error, c echo.Context) {
		code := http.StatusInternalServerError
		var body interface{}

		switch err := err.(type) {
		case common.ErrorResponse:
			code = http.StatusBadRequest
			body = err
		case *common.ErrorResponse:
			code = http.StatusBadRequest
			body = err
		case *echo.HTTPError:
			echoErr := err
			code = echoErr.Code
			body = common.NewErrorResponse(err)
		default:
			body = common.NewErrorResponse(err)
		}

		c.JSON(code, body) // nolint:errcheck
	}

	app.router.GET("/", app.Greeting).Name = "greeting"
	app.router.Static("/docs", "./docs")

	vice := app.router.Group("/vice")
	vice.POST("/launch", app.internal.LaunchAppHandler)
	vice.POST("/apply-labels", app.internal.ApplyAsyncLabelsHandler)
	vice.GET("/async-data", app.internal.AsyncDataHandler)
	vice.GET("/listing", app.internal.FilterableResourcesHandler)
	vice.POST("/:id/download-input-files", app.internal.TriggerDownloadsHandler)
	vice.POST("/:id/save-output-files", app.internal.TriggerUploadsHandler)
	vice.POST("/:id/exit", app.internal.ExitHandler)
	vice.POST("/:id/save-and-exit", app.internal.SaveAndExitHandler)
	vice.GET("/:analysis-id/pods", app.internal.PodsHandler)
	vice.GET("/:analysis-id/logs", app.internal.LogsHandler)
	vice.POST("/:analysis-id/time-limit", app.internal.TimeLimitUpdateHandler)
	vice.GET("/:analysis-id/time-limit", app.internal.GetTimeLimitHandler)
	vice.GET("/:host/url-ready", app.internal.URLReadyHandler)
	vice.GET("/:host/description", app.internal.DescribeAnalysisHandler)

	vicelisting := vice.Group("/listing")
	vicelisting.GET("/", app.internal.FilterableResourcesHandler)
	vicelisting.GET("/deployments", app.internal.FilterableDeploymentsHandler)
	vicelisting.GET("/pods", app.internal.FilterablePodsHandler)
	vicelisting.GET("/configmaps", app.internal.FilterableConfigMapsHandler)
	vicelisting.GET("/services", app.internal.FilterableServicesHandler)
	vicelisting.GET("/ingresses", app.internal.FilterableIngressesHandler)

	viceadmin := vice.Group("/admin")
	viceadmin.GET("/listing", app.internal.AdminFilterableResourcesHandler)
	viceadmin.GET("/:host/description", app.internal.AdminDescribeAnalysisHandler)
	viceadmin.GET("/:host/url-ready", app.internal.AdminURLReadyHandler)

	viceanalyses := viceadmin.Group("/analyses")
	viceanalyses.GET("/", app.internal.AdminFilterableResourcesHandler)
	viceanalyses.POST("/:analysis-id/download-input-files", app.internal.AdminTriggerDownloadsHandler)
	viceanalyses.POST("/:analysis-id/save-output-files", app.internal.AdminTriggerUploadsHandler)
	viceanalyses.POST("/:analysis-id/exit", app.internal.AdminExitHandler)
	viceanalyses.POST("/:analysis-id/save-and-exit", app.internal.AdminSaveAndExitHandler)
	viceanalyses.GET("/:analysis-id/time-limit", app.internal.AdminGetTimeLimitHandler)
	viceanalyses.POST("/:analysis-id/time-limit", app.internal.AdminTimeLimitUpdateHandler)
	viceanalyses.GET("/:analysis-id/external-id", app.internal.AdminGetExternalIDHandler)

	svc := app.router.Group("/service")
	svc.POST("/:name", app.external.CreateServiceHandler)
	svc.PUT("/:name", app.external.UpdateServiceHandler)
	svc.GET("/:name", app.external.GetServiceHandler)
	svc.DELETE("/:name", app.external.DeleteServiceHandler)

	endpoint := app.router.Group("/endpoint")
	endpoint.POST("/:name", app.external.CreateEndpointHandler)
	endpoint.PUT("/:name", app.external.UpdateEndpointHandler)
	endpoint.GET("/:name", app.external.GetEndpointHandler)
	endpoint.DELETE("/:name", app.external.DeleteEndpointHandler)

	ingress := app.router.Group("/ingress")
	ingress.POST("/:name", app.external.CreateIngressHandler)
	ingress.PUT("/:name", app.external.UpdateIngressHandler)
	ingress.GET("/:name", app.external.GetIngressHandler)
	ingress.DELETE("/:name", app.external.DeleteIngressHandler)

	ilgroup := app.router.Group("/instantlaunches")
	app.instantlaunches = instantlaunches.New(app.db, ilgroup, ilInit)

	return app
}

// Greeting lets the caller know that the service is up and should be receiving
// requests.
func (e *ExposerApp) Greeting(context echo.Context) error {
	return context.String(http.StatusOK, "Hello from app-exposer.")
}

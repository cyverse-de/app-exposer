package main

import (
	"net/http"

	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/incluster"
	"github.com/cyverse-de/app-exposer/instantlaunches"
	"github.com/cyverse-de/app-exposer/outcluster"
	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
	"k8s.io/client-go/kubernetes"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	//"github.com/labstack/gommon/log"
)

// ExposerApp encapsulates the overall application-logic, tying together the
// REST-like API with the underlying Kubernetes API. All of the HTTP handlers
// are methods for an ExposerApp instance.
type ExposerApp struct {
	outcluster      *outcluster.Outcluster
	incluster       *incluster.Incluster
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
}

// NewExposerApp creates and returns a newly instantiated *ExposerApp.
func NewExposerApp(init *ExposerAppInit, apps *apps.Apps, conn *nats.EncodedConn, c *koanf.Koanf) *ExposerApp {
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

	inclusterInit := &incluster.Init{
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
		outcluster: outcluster.New(init.ClientSet, init.Namespace, init.IngressClass),
		incluster:  incluster.New(inclusterInit, init.db, init.ClientSet, apps),
		namespace:  init.Namespace,
		clientset:  init.ClientSet,
		router:     echo.New(),
		db:         init.db,
	}

	app.router.Use(otelecho.Middleware("app-exposer"))
	//app.router.Use(middleware.Logger())

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
	vice.Use(middleware.Logger())
	vice.POST("/launch", app.incluster.LaunchAppHandler)
	vice.POST("/apply-labels", app.incluster.ApplyAsyncLabelsHandler)
	vice.GET("/async-data", app.incluster.AsyncDataHandler)
	vice.GET("/listing", app.incluster.FilterableResourcesHandler)
	vice.POST("/:id/download-input-files", app.incluster.TriggerDownloadsHandler)
	vice.POST("/:id/save-output-files", app.incluster.TriggerUploadsHandler)
	vice.POST("/:id/exit", app.incluster.ExitHandler)
	vice.POST("/:id/save-and-exit", app.incluster.SaveAndExitHandler)
	vice.GET("/:analysis-id/pods", app.incluster.PodsHandler)
	vice.GET("/:analysis-id/logs", app.incluster.LogsHandler)
	vice.POST("/:analysis-id/time-limit", app.incluster.TimeLimitUpdateHandler)
	vice.GET("/:analysis-id/time-limit", app.incluster.GetTimeLimitHandler)
	vice.GET("/:host/url-ready", app.incluster.URLReadyHandler)
	vice.GET("/:host/description", app.incluster.DescribeAnalysisHandler)

	vicelisting := vice.Group("/listing")
	vicelisting.GET("/", app.incluster.FilterableResourcesHandler)
	vicelisting.GET("/deployments", app.incluster.FilterableDeploymentsHandler)
	vicelisting.GET("/pods", app.incluster.FilterablePodsHandler)
	vicelisting.GET("/configmaps", app.incluster.FilterableConfigMapsHandler)
	vicelisting.GET("/services", app.incluster.FilterableServicesHandler)
	vicelisting.GET("/ingresses", app.incluster.FilterableIngressesHandler)

	viceadmin := vice.Group("/admin")
	viceadmin.GET("/listing", app.incluster.AdminFilterableResourcesHandler)
	viceadmin.GET("/:host/description", app.incluster.AdminDescribeAnalysisHandler)
	viceadmin.GET("/:host/url-ready", app.incluster.AdminURLReadyHandler)

	viceanalyses := viceadmin.Group("/analyses")
	viceanalyses.GET("/", app.incluster.AdminFilterableResourcesHandler)
	viceanalyses.POST("/:analysis-id/download-input-files", app.incluster.AdminTriggerDownloadsHandler)
	viceanalyses.POST("/:analysis-id/save-output-files", app.incluster.AdminTriggerUploadsHandler)
	viceanalyses.POST("/:analysis-id/exit", app.incluster.AdminExitHandler)
	viceanalyses.POST("/:analysis-id/save-and-exit", app.incluster.AdminSaveAndExitHandler)
	viceanalyses.GET("/:analysis-id/time-limit", app.incluster.AdminGetTimeLimitHandler)
	viceanalyses.POST("/:analysis-id/time-limit", app.incluster.AdminTimeLimitUpdateHandler)
	viceanalyses.GET("/:analysis-id/external-id", app.incluster.AdminGetExternalIDHandler)

	svc := app.router.Group("/service")
	svc.Use(middleware.Logger())
	svc.POST("/:name", app.outcluster.CreateServiceHandler)
	svc.PUT("/:name", app.outcluster.UpdateServiceHandler)
	svc.GET("/:name", app.outcluster.GetServiceHandler)
	svc.DELETE("/:name", app.outcluster.DeleteServiceHandler)

	endpoint := app.router.Group("/endpoint")
	endpoint.Use(middleware.Logger())
	endpoint.POST("/:name", app.outcluster.CreateEndpointHandler)
	endpoint.PUT("/:name", app.outcluster.UpdateEndpointHandler)
	endpoint.GET("/:name", app.outcluster.GetEndpointHandler)
	endpoint.DELETE("/:name", app.outcluster.DeleteEndpointHandler)

	ingress := app.router.Group("/ingress")
	ingress.Use(middleware.Logger())
	ingress.POST("/:name", app.outcluster.CreateIngressHandler)
	ingress.PUT("/:name", app.outcluster.UpdateIngressHandler)
	ingress.GET("/:name", app.outcluster.GetIngressHandler)
	ingress.DELETE("/:name", app.outcluster.DeleteIngressHandler)

	ilgroup := app.router.Group("/instantlaunches")
	ilgroup.Use(middleware.Logger())
	app.instantlaunches = instantlaunches.New(app.db, ilgroup, ilInit)

	return app
}

// Greeting lets the caller know that the service is up and should be receiving
// requests.
func (e *ExposerApp) Greeting(context echo.Context) error {
	return context.String(http.StatusOK, "Hello from app-exposer.")
}

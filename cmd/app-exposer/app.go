package main

import (
	"net/http"

	"github.com/cyverse-de/app-exposer/adapter"
	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/httphandlers"
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

	_ "github.com/cyverse-de/app-exposer/docs"
	echoSwagger "github.com/swaggo/echo-swagger"
	//"github.com/labstack/gommon/log"
)

// ExposerApp encapsulates the overall application-logic, tying together the
// REST-like API with the underlying Kubernetes API. All of the HTTP handlers
// are methods for an ExposerApp instance.
type ExposerApp struct {
	outcluster      *outcluster.Outcluster
	incluster       *incluster.Incluster
	handlers        *httphandlers.HTTPHandlers
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
	batchadapter                  *adapter.JEXAdapter
	ImagePullSecretName           string
	LocalStorageClass             string
}

//	@title			app-exposer
//	@version		1.0
//	@description	The app-exposer API for the Discovery Environment's VICE feature.
//
//	@license.name	3-Clause BSD License
//	@license.url	https://github.com/cyverse-de/app-exposer?tab=License-1-ov-file#readme
//
//	@host			localhost:60000
//	@BasePath		/
//
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
		ImagePullSecretName:           init.ImagePullSecretName,
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
		LocalStorageClass:             init.LocalStorageClass,
	}

	incluster := incluster.New(inclusterInit, init.db, init.ClientSet, apps)

	app := &ExposerApp{
		outcluster: outcluster.New(init.ClientSet, init.Namespace, init.IngressClass),
		incluster:  incluster,
		namespace:  init.Namespace,
		clientset:  init.ClientSet,
		router:     echo.New(),
		db:         init.db,
		handlers:   httphandlers.New(incluster, apps, init.ClientSet, init.batchadapter),
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
	app.router.GET("/docs/*", echoSwagger.WrapHandler)

	batchGroup := app.router.Group("/batch")
	batchGroup.Use(middleware.Logger())
	batchGroup.GET("", app.handlers.BatchHomeHandler)
	batchGroup.GET("/", app.handlers.BatchHomeHandler)
	batchGroup.POST("", app.handlers.BatchLaunchHandler)
	batchGroup.POST("/", app.handlers.BatchLaunchHandler)
	batchGroup.POST("/cleanup", app.handlers.BatchStopByUUID)
	batchGroup.DELETE("/stop/:id", app.handlers.BatchStopHandler)

	info := app.router.Group("/info")
	info.Use(middleware.Logger())
	info.GET("/analysis/status/by/external-id/:external-id", app.handlers.AnalysisStatusByExternalID)
	info.GET("/analysis/status/by/analysis-id/:analysis-id", app.handlers.AnalysisStatusByAnalysisID)

	vice := app.router.Group("/vice")
	vice.Use(middleware.Logger())
	vice.POST("/launch", app.handlers.LaunchAppHandler)
	vice.POST("/apply-labels", app.handlers.ApplyAsyncLabelsHandler)
	vice.GET("/async-data", app.handlers.AsyncDataHandler)
	vice.GET("/listing", app.handlers.FilterableResourcesHandler)
	vice.POST("/:id/download-input-files", app.handlers.TriggerDownloadsHandler)
	vice.POST("/:id/save-output-files", app.handlers.TriggerUploadsHandler)
	vice.POST("/:id/exit", app.handlers.ExitHandler)
	vice.POST("/:id/save-and-exit", app.handlers.SaveAndExitHandler)
	vice.GET("/:analysis-id/pods", app.handlers.PodsHandler)
	vice.GET("/:analysis-id/logs", app.handlers.LogsHandler)
	vice.POST("/:analysis-id/time-limit", app.handlers.TimeLimitUpdateHandler)
	vice.GET("/:analysis-id/time-limit", app.handlers.GetTimeLimitHandler)
	vice.GET("/:host/url-ready", app.handlers.URLReadyHandler)
	vice.GET("/:host/description", app.handlers.DescribeAnalysisHandler)

	vicelisting := vice.Group("/listing")
	vicelisting.GET("/", app.handlers.FilterableResourcesHandler)
	vicelisting.GET("/deployments", app.handlers.FilterableDeploymentsHandler)
	vicelisting.GET("/pods", app.handlers.FilterablePodsHandler)
	vicelisting.GET("/configmaps", app.handlers.FilterableConfigMapsHandler)
	vicelisting.GET("/services", app.handlers.FilterableServicesHandler)
	vicelisting.GET("/ingresses", app.handlers.FilterableIngressesHandler)

	viceadmin := vice.Group("/admin")
	viceadmin.GET("/listing", app.handlers.AdminFilterableResourcesHandler)
	viceadmin.POST("/terminate-all", app.handlers.TerminateAllAnalysesHandler)
	viceadmin.GET("/:host/description", app.handlers.AdminDescribeAnalysisHandler)
	viceadmin.GET("/:host/url-ready", app.handlers.AdminURLReadyHandler)
	viceadmin.GET("/is-deployed/external-id/:external-id", app.handlers.AdminAnalysisInClusterByExternalID)
	viceadmin.GET("/is-deployed/analysis-id/:analysis-id", app.handlers.AdminAnalysisInClusterByID)

	viceanalyses := viceadmin.Group("/analyses")
	viceanalyses.GET("/", app.handlers.AdminFilterableResourcesHandler)
	viceanalyses.POST("/:analysis-id/download-input-files", app.handlers.AdminTriggerDownloadsHandler)
	viceanalyses.POST("/:analysis-id/save-output-files", app.handlers.AdminTriggerUploadsHandler)
	viceanalyses.POST("/:analysis-id/exit", app.handlers.AdminExitHandler)
	viceanalyses.POST("/:analysis-id/save-and-exit", app.handlers.AdminSaveAndExitHandler)
	viceanalyses.GET("/:analysis-id/time-limit", app.handlers.AdminGetTimeLimitHandler)
	viceanalyses.POST("/:analysis-id/time-limit", app.handlers.AdminTimeLimitUpdateHandler)
	viceanalyses.GET("/:analysis-id/external-id", app.handlers.AdminGetExternalIDHandler)

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

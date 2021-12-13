package main

import (
	"net/http"

	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/external"
	"github.com/cyverse-de/app-exposer/instantlaunches"
	"github.com/cyverse-de/app-exposer/internal"
	"github.com/jmoiron/sqlx"
	"k8s.io/client-go/kubernetes"

	"github.com/labstack/echo/v4"
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
	PorklockImage                 string // The image containing the porklock tool
	PorklockTag                   string // The docker tag for the image containing the porklock tool
	UseCSIDriver                  bool   // Yes to use CSI Driver for data input/output, No to use Vice-file-transfer
	InputPathListIdentifier       string // Header line for input path lists
	TicketInputPathListIdentifier string // Header line for ticket input path lists
	ImagePullSecretName           string // A secret name to add to pods' imagePullSecrets
	JobStatusURL                  string
	ViceProxyImage                string
	CASBaseURL                    string
	FrontendBaseURL               string
	ViceDefaultBackendService     string
	ViceDefaultBackendServicePort int
	GetAnalysisIDService          string
	CheckResourceAccessService    string
	VICEBackendNamespace          string
	AppsServiceBaseURL            string
	db                            *sqlx.DB
	UserSuffix                    string
	MetadataBaseURL               string
	PermissionsURL                string
	KeycloakBaseURL               string
	KeycloakRealm                 string
	KeycloakClientID              string
	KeycloakClientSecret          string
}

// NewExposerApp creates and returns a newly instantiated *ExposerApp.
func NewExposerApp(init *ExposerAppInit, ingressClass string, cs kubernetes.Interface, apps *apps.Apps) *ExposerApp {
	internalInit := &internal.Init{
		ViceNamespace:                 init.ViceNamespace,
		PorklockImage:                 init.PorklockImage,
		PorklockTag:                   init.PorklockTag,
		UseCSIDriver:                  init.UseCSIDriver,
		InputPathListIdentifier:       init.InputPathListIdentifier,
		TicketInputPathListIdentifier: init.TicketInputPathListIdentifier,
		ImagePullSecretName:           init.ImagePullSecretName,
		ViceProxyImage:                init.ViceProxyImage,
		CASBaseURL:                    init.CASBaseURL,
		FrontendBaseURL:               init.FrontendBaseURL,
		ViceDefaultBackendService:     init.ViceDefaultBackendService,
		ViceDefaultBackendServicePort: init.ViceDefaultBackendServicePort,
		GetAnalysisIDService:          init.GetAnalysisIDService,
		CheckResourceAccessService:    init.CheckResourceAccessService,
		VICEBackendNamespace:          init.VICEBackendNamespace,
		AppsServiceBaseURL:            init.AppsServiceBaseURL,
		JobStatusURL:                  init.JobStatusURL,
		UserSuffix:                    init.UserSuffix,
		PermissionsURL:                init.PermissionsURL,
		KeycloakBaseURL:               init.KeycloakBaseURL,
		KeycloakRealm:                 init.KeycloakRealm,
		KeycloakClientID:              init.KeycloakClientID,
		KeycloakClientSecret:          init.KeycloakClientSecret,
	}

	app := &ExposerApp{
		external:  external.New(cs, init.Namespace, ingressClass),
		internal:  internal.New(internalInit, init.db, cs, apps),
		namespace: init.Namespace,
		clientset: cs,
		router:    echo.New(),
		db:        init.db,
	}

	ilInit := &instantlaunches.Init{
		UserSuffix:      init.UserSuffix,
		MetadataBaseURL: init.MetadataBaseURL,
		PermissionsURL:  init.PermissionsURL,
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

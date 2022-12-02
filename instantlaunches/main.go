// Package classification instantlaunches
//
// Documentation for the instant launches API.
//
//		Schemes: http
//		BasePath: /instantlaunches
//		Version: 1.0.0
//
//		Consumes:
//		- application/json
//
//		Produces:
//		- application/json
//
// swagger:meta

package instantlaunches

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cyverse-de/app-exposer/permissions"
	"github.com/jmoiron/sqlx"
	"github.com/jmoiron/sqlx/types"
	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

var httpClient = http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}

// InstantLaunch docs
//
// The information needed to instantly launch an app.
//
// swagger:response instantLaunch
//
//	In: body
type InstantLaunch struct {
	// Unique identifier
	//
	// Required: false
	ID string `json:"id" db:"id"`

	// The UUID for the quick launch used in the instant launch.
	//
	// Required: true
	QuickLaunchID string `json:"quick_launch_id" db:"quick_launch_id"`

	// The username of the user that added or is adding the instant launch.
	//
	// Required: true
	AddedBy string `json:"added_by" db:"added_by"` // short username

	// The date the instant launch was added to the system.
	//
	// Required: true
	AddedOn string `json:"added_on" db:"added_on"` // formatted timestamp including timezone
}

// FullInstantLaunch contains more data about the instant launch, including quick launch
// info, the submission, and app info.
type FullInstantLaunch struct {
	ID                     string         `json:"id" db:"id"`
	AddedBy                string         `json:"added_by" db:"added_by"`
	AddedOn                string         `json:"added_on" db:"added_on"`
	QuickLaunchID          string         `json:"quick_launch_id" db:"quick_launch_id"`
	QuickLaunchName        string         `json:"quick_launch_name" db:"ql_name"`
	QuickLaunchDescription string         `json:"quick_launch_description" db:"ql_description"`
	QuickLaunchCreator     string         `json:"quick_launch_creator" db:"ql_creator"`
	QuickLaunchIsPublic    bool           `json:"is_public" db:"is_public"`
	Submission             types.JSONText `json:"submission" db:"submission"`
	AppID                  string         `json:"app_id" db:"app_id"`
	AppVersionID           string         `json:"app_version_id" db:"app_version_id"`
	AppName                string         `json:"app_name" db:"app_name"`
	AppDescription         string         `json:"app_description" db:"app_description"`
	AppVersion             string         `json:"app_version" db:"app_version"`
	AppDeleted             bool           `json:"app_deleted" db:"app_deleted"`
	AppDisabled            bool           `json:"app_disabled" db:"app_disabled"`
	AppIntegrator          string         `json:"integrator" db:"integrator"`
}

// NewInstantLaunchFromJSON instantiates and returns a new *InstantLaunch from the
// ReadCloser passed in. The ReadCloser is closed as part of this function.
func NewInstantLaunchFromJSON(r io.ReadCloser) (*InstantLaunch, error) {
	defer r.Close()

	il := &InstantLaunch{}

	readbytes, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	if err = json.Unmarshal(readbytes, il); err != nil {
		return nil, err
	}

	return il, err
}

// InstantLaunchSelector defines the default and compatible instant launches
// for a file pattern.
type InstantLaunchSelector struct {
	Pattern    string          `json:"pattern"`
	Kind       string          `json:"kind"`
	Default    InstantLaunch   `json:"default"`
	Compatible []InstantLaunch `json:"compatible"`
}

// InstantLaunchMapping maps a pattern string to an InstantLaunchSelector.
type InstantLaunchMapping map[string]*InstantLaunchSelector

// Scan implements the Scan function for database values.
func (i InstantLaunchMapping) Scan(value interface{}) error {
	switch v := value.(type) {
	case []byte:
		json.Unmarshal(v, &i) // nolint:errcheck
		return nil
	case string:
		json.Unmarshal([]byte(v), &i) // nolint:errcheck
		return nil
	default:
		return fmt.Errorf("unsupported type: %T", v)
	}
}

// Value implements the Value() function for database values.
func (i InstantLaunchMapping) Value() (driver.Value, error) {
	return json.Marshal(&i)
}

// InstantLaunchMappingFromJSON creates a new *InstantLaunchMapping from the io.ReadCloser
// passed in. Calls io.ReadAll on the ReadCloser and closes it.
func InstantLaunchMappingFromJSON(r io.ReadCloser) (*InstantLaunchMapping, error) {
	il := &InstantLaunchMapping{}

	defer r.Close()
	readbytes, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	if err = json.Unmarshal(readbytes, il); err != nil {
		return nil, err
	}

	return il, nil
}

// UserInstantLaunchMapping contains the user-specific set of pattern-to-instant-launch
// mappings that override the system-level default set of mappings.
type UserInstantLaunchMapping struct {
	ID      string               `json:"id" db:"id"`
	Version string               `json:"version" db:"version"`
	UserID  string               `json:"user_id" db:"user_id"`
	Mapping InstantLaunchMapping `json:"mapping" db:"mapping"`
}

// QuickLaunch describes an app with a set of pre-filled parameter values.
type QuickLaunch struct {
	ID           string         `json:"id" db:"id"`
	Creator      string         `json:"creator" db:"creator"`
	AppID        string         `json:"app_id" db:"app_id"`
	AppVersionID string         `json:"app_version_id" db:"app_version_id"`
	Name         string         `json:"name" db:"name"`
	Description  string         `json:"description" db:"description"`
	IsPublic     bool           `json:"is_public" db:"is_public"`
	Submission   types.JSONText `json:"submission" db:"submission"`
}

// App provides an API for managing instant launches.
type App struct {
	DB              *sqlx.DB
	Group           *echo.Group
	UserSuffix      string
	MetadataBaseURL string
	Permissions     *permissions.Permissions
}

// Init configuration for the instant launches.
type Init struct {
	UserSuffix      string
	MetadataBaseURL string
	PermissionsURL  string
}

// New returns a newly created *App.
func New(db *sqlx.DB, group *echo.Group, init *Init) *App {
	instance := &App{
		DB:              db,
		Group:           group,
		UserSuffix:      init.UserSuffix,
		MetadataBaseURL: init.MetadataBaseURL,
		Permissions: &permissions.Permissions{
			BaseURL: init.PermissionsURL,
		},
	}

	instance.Group.GET("/quicklaunches/public", instance.ListViablePublicQuickLaunchesHandler)
	instance.Group.GET("/mappings/defaults", instance.ListDefaultsHandler)
	instance.Group.GET("/mappings/defaults/latest", instance.LatestDefaultsHandler)
	instance.Group.PUT("/mappings/defaults/latest", instance.AddLatestDefaultsHandler)
	instance.Group.POST("/mappings/defaults/latest", instance.UpdateLatestDefaultsHandler)
	instance.Group.DELETE("/mappings/defaults/latest", instance.DeleteLatestDefaultsHandler)
	instance.Group.GET("/mappings/defaults/:version", instance.DefaultsByVersionHandler)
	instance.Group.POST("/mappings/defaults/:version", instance.UpdateDefaultsByVersionHandler)
	instance.Group.DELETE("/mappings/defaults/:version", instance.DeleteDefaultsByVersionHandler)
	instance.Group.GET("/mappings/:username", instance.AllUserMappingsHandler)
	instance.Group.GET("/mappings/:username/latest", instance.UserMappingHandler)
	instance.Group.PUT("/mappings/:username", instance.AddUserMappingHandler)
	instance.Group.POST("/mappings/:username/latest", instance.UpdateUserMappingHandler)
	instance.Group.DELETE("/mappings/:username/latest", instance.DeleteUserMappingHandler)
	instance.Group.GET("/mappings/:username/:version", instance.UserMappingsByVersionHandler)
	instance.Group.POST("/mappings/:username/:version", instance.UpdateUserMappingsByVersionHandler)
	instance.Group.DELETE("/mappings/:username/:version", instance.DeleteUserMappingsByVersionHandler)
	instance.Group.GET("/metadata", instance.ListMetadataHandler)
	instance.Group.GET("/metadata/full", instance.FullListMetadataHandler)
	instance.Group.PUT("/", instance.AddInstantLaunchHandler)
	instance.Group.PUT("", instance.AddInstantLaunchHandler)
	instance.Group.GET("/", instance.ListInstantLaunchesHandler)
	instance.Group.GET("/full", instance.FullListInstantLaunchesHandler)
	instance.Group.GET("", instance.ListInstantLaunchesHandler)
	instance.Group.GET("/:id", instance.GetInstantLaunchHandler)
	instance.Group.POST("/:id", instance.UpdateInstantLaunchHandler)
	instance.Group.DELETE("/:id", instance.DeleteInstantLaunchHandler)
	instance.Group.GET("/:id/full", instance.FullInstantLaunchHandler)
	instance.Group.GET("/:id/metadata", instance.GetMetadataHandler)
	instance.Group.POST("/:id/metadata", instance.AddOrUpdateMetadataHandler)
	instance.Group.PUT("/:id/metadata", instance.SetAllMetadataHandler)

	return instance
}

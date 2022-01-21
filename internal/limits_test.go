package internal

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/model"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/apps/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

// The default configuration to use for testing.
var testConfig = &Init{
	PorklockImage:                 "discoenv/porklock",
	PorklockTag:                   "latest",
	UseCSIDriver:                  false,
	InputPathListIdentifier:       "# application/vnd.de.multi-input-path-list+csv; version=1",
	TicketInputPathListIdentifier: "# application/vnd.de.tickets-path-list+csv; version=1",
	ViceProxyImage:                "discoenv/vice-proxy",
	CASBaseURL:                    "https://auth.example.org/cas",
	FrontendBaseURL:               "https://example.run",
	ViceDefaultBackendService:     "vice-default-backend",
	ViceDefaultBackendServicePort: 80,
	GetAnalysisIDService:          "get-analysis-id",
	CheckResourceAccessService:    "check-resource-access",
	VICEBackendNamespace:          "de",
	AppsServiceBaseURL:            "http://apps",
	ViceNamespace:                 "vice-apps",
	JobStatusURL:                  "http://job-status-listener",
	UserSuffix:                    "@example.org",
}

// viceDeployment creates a fake VICE deployment to use for testing.
func viceDeployment(n int, namespace, username string, externalID *string) *v1.Deployment {
	labels := map[string]string{"username": labelValueString(username)}
	if externalID != nil {
		labels["external-id"] = *externalID
	}
	return &v1.Deployment{
		ObjectMeta: meta_v1.ObjectMeta{
			Namespace: namespace,
			Name:      fmt.Sprintf("analysis %d", n),
			Labels:    labels,
		},
	}
}

// setupInternal sets up an instance of Internal to use for testing.
func setupInternal(t *testing.T, objs []runtime.Object) (*Internal, sqlmock.Sqlmock) {
	mockdb, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal("unable to create the mock database")
	}
	sqlxMockDB := sqlx.NewDb(mockdb, "sqlmock")

	client := fake.NewSimpleClientset(objs...)

	apps := apps.NewApps(sqlxMockDB, "@iplantcollaborative.org")

	internal := New(testConfig, sqlxMockDB, client, apps)
	return internal, mock
}

// intPointer is just a helper function to return a pointer to an integer.
func intPointer(value int) *int {
	return &value
}

// stringPointer is just a helper function to return a pointer to a string.
func stringPointer(value string) *string {
	return &value
}

// registerLimitQuery registers a job limit query for a user.
func registerLimitQuery(mock sqlmock.Sqlmock, username string, limit *int) {
	rows := mock.NewRows([]string{"concurrent_jobs"})
	if limit != nil {
		rows.AddRow(fmt.Sprintf("%d", *limit))
	}
	mock.ExpectQuery("SELECT concurrent_jobs FROM job_limits WHERE launcher =").
		WithArgs(username).
		WillReturnRows(rows)
}

// registerDefaultLimitQuery registers the default job limit query.
func registerDefaultLimitQuery(mock sqlmock.Sqlmock, limit int) {
	rows := mock.NewRows([]string{"concurrent_jobs"}).AddRow(fmt.Sprintf("%d", limit))
	mock.ExpectQuery("SELECT concurrent_jobs FROM job_limits WHERE launcher IS NULL").
		WillReturnRows(rows)
}

// registerAnalysisIDQuery registers the query to get the analysis ID for an external ID
// if that an external ID is provided. If no external ID is provided then we assume that
// no query should be performed.
func registerAnalysisIDQuery(mock sqlmock.Sqlmock, externalID, analysisID *string) {
	if externalID != nil {
		rows := mock.NewRows([]string{"id"})
		if analysisID != nil {
			rows.AddRow(*analysisID)
		}
		mock.ExpectQuery("SELECT j.id FROM jobs j JOIN job_steps s ON s.job_id = j.id").
			WithArgs(*externalID).
			WillReturnRows(rows)
	}
}

// registerAnalysisStatusQuery registers the query to get the status of an analysis ID
// if an analysis ID is provided. If no analysis ID is provided then we assume that no
// query should be performed.
func registerAnalysisStatusQuery(mock sqlmock.Sqlmock, analysisID, status *string) {
	if analysisID != nil {
		rows := mock.NewRows([]string{"status"})
		if status != nil {
			rows.AddRow(*status)
		}
		mock.ExpectQuery("SELECT j.status FROM jobs j").WithArgs(analysisID).WillReturnRows(rows)
	}
}

// expectedLimitError builds the expected error code for the given values.
func expectedLimitError(user string, defaultJobLimit, jobCount int, jobLimit *int) error {
	switch {

	// Jobs are disabled by default and the user has not been granted permission yet.
	case jobLimit == nil && defaultJobLimit <= 0:
		code := "ERR_PERMISSION_NEEDED"
		msg := fmt.Sprintf("%s has not been granted permission to run jobs yet", user)
		return buildLimitError(code, msg, defaultJobLimit, jobCount, jobLimit)

	// Jobs have been explicitly disabled for the user.
	case jobLimit != nil && *jobLimit <= 0:
		code := "ERR_FORBIDDEN"
		msg := fmt.Sprintf("%s is not permitted to run jobs", user)
		return buildLimitError(code, msg, defaultJobLimit, jobCount, jobLimit)

	// The user is using and has reached the default job limit.
	case jobLimit == nil && jobCount >= defaultJobLimit:
		code := "ERR_LIMIT_REACHED"
		msg := fmt.Sprintf("%s is already running %d or more concurrent jobs", user, defaultJobLimit)
		return buildLimitError(code, msg, defaultJobLimit, jobCount, jobLimit)

	// The user has explicitly been granted the ability to run jobs and has reached the limit.
	case jobLimit != nil && jobCount >= *jobLimit:
		code := "ERR_LIMIT_REACHED"
		msg := fmt.Sprintf("%s is already running %d or more concurrent jobs", user, *jobLimit)
		return buildLimitError(code, msg, defaultJobLimit, jobCount, jobLimit)

	// In every other case, we can permit the job to be launched.
	default:
		return nil
	}
}

// createTestSubmission creates a job submission for testing.
func createTestSubmission(username string) *model.Job {
	return &model.Job{
		ExecutionTarget: "interapps",
		Submitter:       username,
	}
}

type analysisRecord struct {
	externalID *string
	analysisID *string
	status     *string
}

type limitTest struct {
	description  string
	username     string
	analyses     []analysisRecord
	limit        *int
	defaultLimit int
}

var testAnalyses = []analysisRecord{
	{
		externalID: stringPointer("d24b8885-ddfb-4192-96aa-03d127576e51"),
		analysisID: stringPointer("604215e8-019c-4ca7-9141-e8fd0f5c9088"),
		status:     stringPointer("Running"),
	},
	{
		externalID: stringPointer("4056f3dc-5829-4960-bbcc-ccd11c650843"),
		analysisID: stringPointer("a6e54728-fb25-40b8-908e-83c6616d3bf1"),
		status:     stringPointer("Running"),
	},
}

func TestLimitChecks(t *testing.T) {
	tests := []limitTest{
		{
			description:  "default limit not reached",
			username:     "foo",
			analyses:     testAnalyses[0:1],
			limit:        nil,
			defaultLimit: 2,
		},
		{
			description:  "explicit limit not reached",
			username:     "foo",
			analyses:     testAnalyses[0:1],
			limit:        intPointer(2),
			defaultLimit: 0,
		},
		{
			description:  "default limit reached",
			username:     "foo",
			analyses:     testAnalyses,
			limit:        nil,
			defaultLimit: 2,
		},
		{
			description:  "explicit limit reached",
			username:     "foo",
			analyses:     testAnalyses,
			limit:        intPointer(2),
			defaultLimit: 0,
		},
		{
			description:  "explicit permission not granted",
			username:     "foo",
			analyses:     []analysisRecord{},
			limit:        nil,
			defaultLimit: 0,
		},
		{
			description:  "banned user",
			username:     "foo",
			analyses:     []analysisRecord{},
			limit:        intPointer(0),
			defaultLimit: 0,
		},
		{
			description:  "username containing a trailing underscore",
			username:     "foo_",
			analyses:     []analysisRecord{},
			limit:        intPointer(2),
			defaultLimit: 0,
		},
		{
			description:  "username containing multiple trailing underscores",
			username:     "foo____",
			analyses:     []analysisRecord{},
			limit:        intPointer(2),
			defaultLimit: 0,
		},
		{
			description:  "username containing a leading underscore",
			username:     "_foo",
			analyses:     []analysisRecord{},
			limit:        intPointer(2),
			defaultLimit: 0,
		},
		{
			description:  "username containing multiple leading underscores",
			username:     "____foo",
			analyses:     []analysisRecord{},
			limit:        intPointer(2),
			defaultLimit: 0,
		},
		{
			description:  "username containing a bunch of underscores and hyphens",
			username:     "____foo__bar--baz__quux____",
			analyses:     []analysisRecord{},
			limit:        intPointer(2),
			defaultLimit: 0,
		},
	}

	// Run the tests.
	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			assert := assert.New(t)

			// Prepare the list of k8s objects.
			objs := make([]runtime.Object, len(test.analyses))
			for i, analysis := range test.analyses {
				objs[i] = viceDeployment(i, "vice-apps", test.username, analysis.externalID)
			}

			// Create all of the mocks.
			internal, mock := setupInternal(t, objs)
			defer internal.db.Close()

			// Add the database expectations.
			for _, analysis := range test.analyses {
				registerAnalysisIDQuery(mock, analysis.externalID, analysis.analysisID)
				registerAnalysisStatusQuery(mock, analysis.analysisID, analysis.status)
			}
			registerLimitQuery(mock, test.username, test.limit)
			registerDefaultLimitQuery(mock, test.defaultLimit)

			// Run the limit check.
			status, err := internal.validateJob(createTestSubmission(test.username))
			expectedError := expectedLimitError(test.username, test.defaultLimit, len(test.analyses), test.limit)
			if expectedError == nil {
				assert.Equalf(http.StatusOK, status, "the status code should be %d", http.StatusOK)
				assert.NoError(err, "no error should be returned")
			} else {
				assert.Equalf(http.StatusBadRequest, status, "the status code should be %d", http.StatusBadRequest)
				assert.Equal(expectedError, err, "the correct error should be returned")
			}
			assert.NoError(mock.ExpectationsWereMet(), "the correct queries should be executed")
		})
	}
}

func TestLabelValueReplacement(t *testing.T) {
	assert := assert.New(t)

	assert.Equal("foo-xxx-u", labelValueString("foo_"))
	assert.Equal("foo-xxx-u-u", labelValueString("foo__"))
	assert.Equal("foo-xxx-u-h-u", labelValueString("foo_-_"))
	assert.Equal("h-xxx-foo", labelValueString("-foo"))
	assert.Equal("h-u-h-xxx-foo", labelValueString("-_-foo"))
	assert.Equal("h-u-h-xxx-foo-bar-xxx-h-u-h", labelValueString("-_-foo-bar-_-"))
	assert.Equal("u-u-u-xxx-foo_bar-xxx-u-u-u", labelValueString("___foo_bar___"))
	assert.Equal("u-u-u-u-xxx-foo__bar-baz__quux-xxx-u-u-u-u", labelValueString("____foo__bar--baz__quux____"))
}

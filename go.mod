module github.com/cyverse-de/app-exposer

go 1.12

require (
	github.com/DATA-DOG/go-sqlmock v1.5.0
	github.com/cockroachdb/apd v1.1.0
	github.com/cyverse-de/configurate v0.0.0-20210914212501-fc18b48e00a9
	github.com/cyverse-de/messaging/v9 v9.1.1
	github.com/cyverse-de/model v0.0.0-20211027151045-62de96618208
	github.com/google/go-cmp v0.5.7
	github.com/google/uuid v1.3.0
	github.com/gosimple/slug v1.10.0
	github.com/jmoiron/sqlx v1.3.4
	github.com/labstack/echo/v4 v4.7.0
	github.com/lib/pq v1.10.3
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.8.1
	github.com/spf13/viper v1.9.0
	github.com/stretchr/testify v1.7.1
	github.com/uptrace/opentelemetry-go-extra/otelsql v0.1.10
	github.com/uptrace/opentelemetry-go-extra/otelsqlx v0.1.10
	github.com/valyala/fastjson v1.6.3
	go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho v0.30.0
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.30.0
	go.opentelemetry.io/otel v1.6.0
	go.opentelemetry.io/otel/exporters/jaeger v1.6.0
	go.opentelemetry.io/otel/sdk v1.6.0
	go.opentelemetry.io/otel/trace v1.6.0
	golang.org/x/crypto v0.0.0-20210921155107-089bfa567519 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	k8s.io/api v0.23.5
	k8s.io/apimachinery v0.23.5
	k8s.io/client-go v0.23.5
	k8s.io/klog v1.0.0
)

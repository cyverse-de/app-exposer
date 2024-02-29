module github.com/cyverse-de/app-exposer

go 1.18

require (
	github.com/DATA-DOG/go-sqlmock v1.5.0
	github.com/cockroachdb/apd v1.1.0
	github.com/cyverse-de/go-mod/cfg v0.0.1
	github.com/cyverse-de/go-mod/gotelnats v0.0.11
	github.com/cyverse-de/go-mod/logging v0.0.2
	github.com/cyverse-de/go-mod/otelutils v0.0.2
	github.com/cyverse-de/go-mod/pbinit v0.0.11
	github.com/cyverse-de/go-mod/protobufjson v0.0.3
	github.com/cyverse-de/messaging/v9 v9.1.5
	github.com/cyverse-de/model/v6 v6.0.1
	github.com/cyverse-de/p/go/qms v0.0.12
	github.com/google/go-cmp v0.5.9
	github.com/google/uuid v1.3.0
	github.com/gosimple/slug v1.13.1
	github.com/jmoiron/sqlx v1.3.5
	github.com/knadh/koanf v1.4.4
	github.com/labstack/echo/v4 v4.9.1
	github.com/lib/pq v1.10.7
	github.com/nats-io/nats.go v1.20.0
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.9.0
	github.com/stretchr/testify v1.8.1
	github.com/uptrace/opentelemetry-go-extra/otelsql v0.1.10
	github.com/uptrace/opentelemetry-go-extra/otelsqlx v0.1.10
	github.com/valyala/fastjson v1.6.3
	go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho v0.30.0
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.30.0
	go.opentelemetry.io/otel v1.11.1
	go.opentelemetry.io/otel/trace v1.11.1
	k8s.io/api v0.23.5
	k8s.io/apimachinery v0.23.5
	k8s.io/client-go v0.23.5
	k8s.io/klog v1.0.0
)

require (
	github.com/cyverse-de/configurate v0.0.0-20210914212501-fc18b48e00a9 // indirect
	github.com/cyverse-de/p/go/analysis v0.0.13 // indirect
	github.com/cyverse-de/p/go/header v0.0.1 // indirect
	github.com/cyverse-de/p/go/monitoring v0.0.2 // indirect
	github.com/cyverse-de/p/go/svcerror v0.0.5 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/felixge/httpsnoop v1.0.2 // indirect
	github.com/fsnotify/fsnotify v1.6.0 // indirect
	github.com/go-logr/logr v1.2.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt v3.2.2+incompatible // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/gofuzz v1.1.0 // indirect
	github.com/googleapis/gnostic v0.5.5 // indirect
	github.com/gosimple/unidecode v1.0.1 // indirect
	github.com/hashicorp/hcl v1.0.0 // indirect
	github.com/imdario/mergo v0.3.5 // indirect
	github.com/joho/godotenv v1.3.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/labstack/gommon v0.4.0 // indirect
	github.com/magiconair/properties v1.8.6 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.16 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/nats-io/nats-server/v2 v2.8.4 // indirect
	github.com/nats-io/nkeys v0.3.0 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/pelletier/go-toml v1.9.5 // indirect
	github.com/pelletier/go-toml/v2 v2.0.6 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/spf13/afero v1.9.3 // indirect
	github.com/spf13/cast v1.5.0 // indirect
	github.com/spf13/jwalterweatherman v1.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/spf13/viper v1.14.0 // indirect
	github.com/streadway/amqp v1.0.1-0.20200716223359-e6b33f460591 // indirect
	github.com/subosito/gotenv v1.4.1 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	go.opentelemetry.io/otel/exporters/jaeger v1.6.1 // indirect
	go.opentelemetry.io/otel/internal/metric v0.27.0 // indirect
	go.opentelemetry.io/otel/metric v0.27.0 // indirect
	go.opentelemetry.io/otel/sdk v1.6.1 // indirect
	golang.org/x/crypto v0.14.0 // indirect
	golang.org/x/net v0.17.0 // indirect
	golang.org/x/oauth2 v0.0.0-20221014153046-6fdb5e3db783 // indirect
	golang.org/x/sys v0.13.0 // indirect
	golang.org/x/term v0.13.0 // indirect
	golang.org/x/text v0.13.0 // indirect
	golang.org/x/time v0.0.0-20220609170525-579cf78fd858 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/protobuf v1.28.1 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/klog/v2 v2.30.0 // indirect
	k8s.io/kube-openapi v0.0.0-20211115234752-e816edb12b65 // indirect
	k8s.io/utils v0.0.0-20211116205334-6203023598ed // indirect
	sigs.k8s.io/json v0.0.0-20211020170558-c049b76a60c6 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.2.1 // indirect
	sigs.k8s.io/yaml v1.2.0 // indirect
)

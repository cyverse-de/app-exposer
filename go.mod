module github.com/cyverse-de/app-exposer

go 1.12

replace github.com/cyverse-de/app-exposer/apps => ./apps

replace github.com/cyverse-de/app-exposer/common => ./common

replace github.com/cyverse-de/app-exposer/external => ./external

replace github.com/cyverse-de/app-exposer/instantlaunches => ./instantlaunches

replace github.com/cyverse-de/app-exposer/internal => ./internal

replace github.com/cyverse-de/app-exposer/permissions => ./permissions

require (
	github.com/DATA-DOG/go-sqlmock v1.5.0
	github.com/cyverse-de/configurate v0.0.0-20190318152107-8f767cb828d9
	github.com/cyverse-de/messaging v6.0.0+incompatible
	github.com/cyverse-de/model v0.0.0-20210826203231-e2d6ebd26e07
	github.com/fsnotify/fsnotify v1.5.1 // indirect
	github.com/google/go-cmp v0.5.5
	github.com/googleapis/gnostic v0.1.0 // indirect
	github.com/gosimple/slug v1.5.0
	github.com/jmoiron/sqlx v1.2.0
	github.com/konsorten/go-windows-terminal-sequences v1.0.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/labstack/echo/v4 v4.1.17
	github.com/lib/pq v1.2.0
	github.com/mattn/go-colorable v0.1.8 // indirect
	github.com/niemeyer/pretty v0.0.0-20200227124842-a10e7caefd8e // indirect
	github.com/pkg/errors v0.9.1
	github.com/rainycape/unidecode v0.0.0-20150907023854-cb7f23ec59be // indirect
	github.com/sirupsen/logrus v1.4.2
	github.com/spf13/cast v1.4.1 // indirect
	github.com/spf13/viper v1.8.1
	github.com/streadway/amqp v0.0.0-20190404075320-75d898a42a94 // indirect
	github.com/stretchr/testify v1.7.0
	github.com/valyala/fastjson v1.6.3
	golang.org/x/crypto v0.0.0-20201016220609-9e8e0b390897 // indirect
	golang.org/x/sys v0.0.0-20210823070655-63515b42dcdf // indirect
	golang.org/x/text v0.3.7 // indirect
	gopkg.in/check.v1 v1.0.0-20200227125254-8fa46927fb4f // indirect
	gopkg.in/cyverse-de/model.v4 v4.0.0-20191010001558-736b5a572acd // indirect
	k8s.io/api v0.17.0
	k8s.io/apimachinery v0.17.0
	k8s.io/client-go v0.17.0
	k8s.io/klog v1.0.0
)

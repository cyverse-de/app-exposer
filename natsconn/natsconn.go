package natsconn

import (
	"time"

	"github.com/labstack/gommon/log"
	"github.com/nats-io/nats.go"
)

type Init struct {
	NATSCluster       string
	NATSTLSKey        string
	NATSTLSCert       string
	NATSTLSCA         string
	NATSCredsFilePath string
	NATSMaxReconnects int
	NATSReconnectWait int
}

func New(init *Init) (*nats.EncodedConn, error) {
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
		return nil, err
	}
	// log.Infof("configured servers: %s", strings.Join(nc.Servers(), " "))
	// log.Infof("connected to NATS host: %s", nc.ConnectedServerName())

	conn, err := nats.NewEncodedConn(nc, "protojson")
	if err != nil {
		return nil, err
	}
	return conn, nil
}

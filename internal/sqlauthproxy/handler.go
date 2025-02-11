package sqlauthproxy

import (
	"context"
	"fmt"
	"net"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/cloudsql"
)

type handler interface {
	handleClient(c net.Conn)
}

type Config struct {
	Hostname         string
	Port             int
	RdsIam           bool
	Username         string
	Password         string
	UpstreamType     string
	UpstreamCAFile   string
	UpstreamCertFile string
	UpstreamKeyFile  string
	UpstreamTLS      bool
	AwsRegion        string
	DialerFunc       func(context.Context, string, string) (net.Conn, error)
}

func Serve(l net.Listener, config Config) error {
	var handler handler
	var err error

	switch config.UpstreamType {
	case "postgres":
		handler, err = newPostgresHandler(config)
		if err != nil {
			return fmt.Errorf("sqlauthproxy: %s", err)
		}
	default:
		handler, err = newMysqlHandler(config)
		if err != nil {
			return fmt.Errorf("sqlauthproxy: %s", err)
		}
	}

	for {
		rconn, err := l.Accept()
		if err != nil {
			return fmt.Errorf("sqlauthproxy: failed to accept connection: %s", err)
		}

		go handler.handleClient(rconn)
	}
}

func BuildHandlerConfig(socket models.Socket) (*Config, error) {
	upstreamTLS := true
	if socket.ConnectorLocalData.UpstreamTLS != nil {
		upstreamTLS = *socket.ConnectorLocalData.UpstreamTLS
	}

	handlerConfig := &Config{
		Hostname:         socket.ConnectorData.TargetHostname,
		Port:             socket.ConnectorData.Port,
		RdsIam:           socket.ConnectorLocalData.RdsIAMAuth,
		Username:         socket.ConnectorLocalData.UpstreamUsername,
		Password:         socket.ConnectorLocalData.UpstreamPassword,
		UpstreamType:     socket.UpstreamType,
		AwsRegion:        socket.ConnectorLocalData.AWSRegion,
		UpstreamCAFile:   socket.ConnectorLocalData.UpstreamCACertFile,
		UpstreamCertFile: socket.ConnectorLocalData.UpstreamCertFile,
		UpstreamKeyFile:  socket.ConnectorLocalData.UpstreamKeyFile,
		UpstreamTLS:      upstreamTLS,
	}

	if socket.ConnectorLocalData.CloudSQLConnector {
		if socket.ConnectorLocalData.CloudSQLInstance == "" {
			return nil, fmt.Errorf("cloudsql instance is not defined")
		}

		ctx := context.Background()
		dialer, err := cloudsql.NewDialer(ctx, socket.ConnectorLocalData.CloudSQLInstance, socket.ConnectorLocalData.GoogleCredentialsFile, socket.ConnectorLocalData.CloudSQLIAMAuth)
		if err != nil {
			return nil, fmt.Errorf("failed to create dialer for cloudSQL: %s", err)
		}

		handlerConfig.DialerFunc = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.Dial(ctx, socket.ConnectorLocalData.CloudSQLInstance)
		}
	}

	return handlerConfig, nil
}

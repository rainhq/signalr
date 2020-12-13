package signalr

import (
	"net/http"
	"net/url"
	"time"

	"github.com/cenkalti/backoff/v4"
)

type DialOpt func(*config)

func HTTPClient(client *http.Client) DialOpt {
	return func(c *config) {
		c.Client = client
	}
}

func Dialer(dialer WebsocketDialerFunc) DialOpt {
	return func(c *config) {
		c.Dialer = dialer
	}
}

func Protocol(protocol string) DialOpt {
	return func(c *config) {
		c.Protocol = protocol
	}
}

func Params(params url.Values) DialOpt {
	return func(c *config) {
		c.Params = params
	}
}

func Headers(headers http.Header) DialOpt {
	return func(c *config) {
		c.Headers = headers
	}
}

// The maximum number of times to re-attempt a negotiation.
func MaxNegotiateRetries(retries int) DialOpt {
	return func(c *config) {
		c.MaxNegotiateRetries = retries
	}
}

// The maximum number of times to re-attempt a connection.
func MaxConnectRetries(retries int) DialOpt {
	return func(c *config) {
		c.MaxConnectRetries = retries
	}
}

func MaxReconnectRetries(retries int) DialOpt {
	return func(c *config) {
		c.MaxReconnectRetries = retries
	}
}

// The maximum number of times to re-attempt a start command.
func MaxStartRetries(retries int) DialOpt {
	return func(c *config) {
		c.MaxStartRetries = retries
	}
}

// The time to wait before retrying, in the event that an error occurs
// when contacting the SignalR service.
func RetryInterval(interval time.Duration) DialOpt {
	return func(c *config) {
		c.RetryInterval = interval
	}
}

// The maximum amount of time to spend retrying a reconnect attempt.
func MaxReconnectDuration(duration time.Duration) DialOpt {
	return func(c *config) {
		c.MaxReconnectDuration = duration
	}
}

type config struct {
	Client               *http.Client
	Dialer               WebsocketDialerFunc
	Protocol             string
	Params               url.Values
	Headers              http.Header
	MaxNegotiateRetries  int
	MaxConnectRetries    int
	MaxReconnectRetries  int
	MaxReconnectDuration time.Duration
	MaxStartRetries      int
	RetryInterval        time.Duration
}

func (c config) NegotiateBackoff() backoff.BackOff {
	return constantBackoff(c.RetryInterval, c.MaxNegotiateRetries)
}

func (c config) ConnectBackoff() backoff.BackOff {
	return constantBackoff(c.RetryInterval, c.MaxConnectRetries)
}

func (c config) ReconnectBackoff() backoff.BackOff {
	return constantBackoff(c.RetryInterval, c.MaxReconnectRetries)
}

func (c config) StartBackoff() backoff.BackOff {
	return constantBackoff(c.RetryInterval, c.MaxStartRetries)
}

var defaultConfig = config{
	Client:               http.DefaultClient,
	Dialer:               NewDefaultDialer,
	Protocol:             "1.5",
	Params:               make(url.Values),
	Headers:              make(http.Header),
	MaxNegotiateRetries:  5,
	MaxConnectRetries:    5,
	MaxReconnectRetries:  5,
	MaxReconnectDuration: 5 * time.Minute,
	MaxStartRetries:      5,
	RetryInterval:        1 * time.Second,
}

func constantBackoff(interval time.Duration, maxRetries int) backoff.BackOff {
	return backoff.WithMaxRetries(
		backoff.NewConstantBackOff(interval),
		uint64(maxRetries),
	)
}

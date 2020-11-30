[![PkgGoDev](https://pkg.go.dev/badge/github.com/rainhq/signalr/v2)](https://pkg.go.dev/github.com/rainhq/signalr/v2)

# Overview

This is my personal attempt at implementating the client side of the WebSocket
portion of the SignalR protocol. I use it for various virtual currency trading
platforms that use SignalR.

## Examples

Simple example:

```go
package main

import (
	"log"

	"github.com/rainhq/signalr/v2"
)

func main() {
	c := signalr.New(
		"fake-server.definitely-not-real",
		"1.5",
		"/signalr",
		`[{"name":"awesomehub"}]`,
		nil,
	)

	// Define handlers.
	msgHandler := func(_ context.Context, msg signalr.Message) error {
		log.Println(msg)
		return nil
	}

	ctx := context.Background()

	// Start the run loop.
	if err := c.Run(ctx, msgHandler); err != nil {
		log.Fatal(err)
	}
}
```

Generic usage:

- [Basic usage](https://github.com/rainhq/signalr/v2/blob/master/examples/basic/main.go)
- [Complex usage](https://github.com/rainhq/signalr/v2/blob/master/examples/complex/main.go)

Cryptocurrency examples:

- [Bittrex](https://github.com/rainhq/signalr/v2/blob/master/examples/bittrex/main.go)
- [Cryptopia](https://github.com/rainhq/signalr/v2/blob/master/examples/cryptopia/main.go)

Proxy examples:

- [No authentication](https://github.com/rainhq/signalr/v2/blob/master/examples/proxy-simple)
- [With authentication](https://github.com/rainhq/signalr/v2/blob/master/examples/proxy-authenticated)

# Documentation

- SignalR specification: https://docs.microsoft.com/en-us/aspnet/signalr/overview/
- Excellent technical deep dive of the protocol: https://blog.3d-logic.com/2015/03/29/signalr-on-the-wire-an-informal-description-of-the-signalr-protocol/

# Contribute

If anything is unclear or could be improved, please open an issue or submit a
pull request. Thanks!

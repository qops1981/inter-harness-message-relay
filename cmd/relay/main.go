package main

import (
	"context"
	"os"

	"github.com/qops1981/inter-harness-message-relay/internal/relay"
)

func main() {
	os.Exit(relay.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

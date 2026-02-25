package main

import (
	"fmt"
	"os"

	"github.com/amurg-ai/amurg/hub"
	"github.com/amurg-ai/amurg/hub/cli"
)

var version = "dev"

func main() {
	root := cli.NewRootCmd(version, hub.Options{})
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

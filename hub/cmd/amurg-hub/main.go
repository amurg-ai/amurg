package main

import (
	"fmt"
	"os"

	"github.com/amurg-ai/amurg/hub/internal/cmd"
)

var version = "dev"

func main() {
	root := cmd.NewRootCmd(version)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

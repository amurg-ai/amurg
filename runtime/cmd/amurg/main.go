package main

import (
	"fmt"
	"os"

	"github.com/amurg-ai/amurg/runtime/internal/usercmd"
)

var version = "dev"

func main() {
	root := usercmd.NewRootCmd(version)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

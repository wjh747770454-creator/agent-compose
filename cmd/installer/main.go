package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if err := newRootCommand(os.Stdout, os.Stderr).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

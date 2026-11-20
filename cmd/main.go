package main

import (
	"os"
	"sathiraumesh/ampere/internal/cli"
)

func main() {
	if err := cli.RootCommand().Execute(); err != nil {
		os.Exit(1) // cobra has already printed the error
	}
}

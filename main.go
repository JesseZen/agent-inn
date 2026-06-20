package main

import (
	"os"

	"github.com/jesse/codex-app-proxy/cmd"
)

func main() {
	os.Exit(cmd.Run(os.Args[1:], os.Stdout, os.Stderr))
}

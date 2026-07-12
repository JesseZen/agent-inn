package main

import (
	"os"
	"runtime/debug"

	"github.com/jesse/agent-inn/cmd"
)

func main() {
	debug.SetTraceback("all")
	os.Exit(cmd.Run(os.Args[1:], os.Stdout, os.Stderr))
}

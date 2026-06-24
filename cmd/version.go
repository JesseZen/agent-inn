package cmd

import (
	"fmt"
	"io"
)

var version = "dev"

func runVersion(stdout io.Writer) int {
	fmt.Fprintf(stdout, "ainn %s\n", version)
	return 0
}

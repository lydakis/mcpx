package main

import (
	"fmt"
	"os"

	"github.com/lydakis/mcpx/internal/cli"
	"github.com/lydakis/mcpx/internal/daemon"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "__daemon" {
		if err := daemon.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "mcpx daemon: %v\n", err)
			os.Exit(1)
		}
		return
	}

	code := cli.Run(os.Args[1:])
	os.Exit(code)
}

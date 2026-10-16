package main

import (
	"os"

	"github.com/danielfnfaria/agtop/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], cli.OSSystem(), os.Stdout))
}

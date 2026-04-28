package main

import (
	"os"

	"github.com/volodymyrsmirnov/choix/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:]))
}

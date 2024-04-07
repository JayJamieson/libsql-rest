package main

import (
	"fmt"
	"os"

	"github.com/JayJamieson/libsql-rest/internal/cmd"
)

func run() error {
	return cmd.Execute()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

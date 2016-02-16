package main

import (
	"fmt"
	"os"

	"github.com/giantswarm/formica/cli"
)

func main() {
	if err := cli.MainCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(-1)
	}
}

// Package main is the supeux CLI entry point.
//
// supeux is an application-definition compiler that emits Terraform/HCL
// (cloud targets) or docker-compose (local targets) from a single
// supeux.yaml. See docs/poc_yaml_spec.md for the yaml shape and
// docs/compiler-language.md for why the CLI is in Go.
//
// This is a scaffolding stub; the actual compiler pipeline is not
// implemented yet.
package main

import (
	"fmt"
	"os"
)

const version = "0.0.0-pre-scoping"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println("supeux", version)
		return
	}
	fmt.Fprintln(os.Stderr, "supeux: not implemented yet")
	fmt.Fprintln(os.Stderr, "see docs/poc_yaml_spec.md for the planned yaml shape")
	os.Exit(2)
}

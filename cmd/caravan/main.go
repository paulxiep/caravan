// Package main is the caravan CLI entry point.
//
// caravan is an application-definition compiler that emits Terraform/HCL
// (cloud targets) or docker-compose (local targets) from a single
// caravan.yaml. See docs/poc_yaml_spec.md for the yaml shape and
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
		fmt.Println("caravan", version)
		return
	}
	fmt.Fprintln(os.Stderr, "caravan: not implemented yet")
	fmt.Fprintln(os.Stderr, "see docs/poc_yaml_spec.md for the planned yaml shape")
	os.Exit(2)
}

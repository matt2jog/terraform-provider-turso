package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/matt2jog/terraform-provider-turso/internal/provider"
)

var version = "dev"
var commit = "unknown"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with debugger support")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/matt2jog/turso",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}

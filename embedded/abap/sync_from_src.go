//go:build ignore

// sync_from_src copies the ZADT_VSP sources that vsp install deploys from
// src/ into this directory, where go:embed can reach them. Run it through
//
//	go generate ./embedded/abap
//
// It imports the package it writes into, so an object newly added to
// GetObjects needs its //go:embed file to exist (empty is enough) before the
// first run.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	embedded "github.com/oisee/vibing-steampunk/embedded/abap"
)

func main() {
	for _, obj := range embedded.GetObjects() {
		name := embedded.FileName(obj)
		data, err := os.ReadFile(filepath.Join("..", "..", "src", name))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", obj.Name, err)
			os.Exit(1)
		}
		// Written with LF, as the repository stores it, whatever the checkout.
		out := strings.ReplaceAll(string(data), "\r\n", "\n")
		if err := os.WriteFile(name, []byte(out), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
			os.Exit(1)
		}
		fmt.Printf("src/%s -> embedded/abap/%s\n", name, name)
	}
}

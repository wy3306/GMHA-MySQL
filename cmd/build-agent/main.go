// build-agent publishes versioned Agent binaries to the Manager package store.
package main

import (
	"context"
	"flag"
	"fmt"
	"gmha/internal/app"
	"log"
	"os"
	"path/filepath"
	"time"
)

func main() {
	home, _ := os.UserHomeDir()
	settings := flag.String("settings", filepath.Join(home, ".gmha", "package-store.json"), "Manager package settings file")
	storage := flag.String("storage", "", "override package storage directory")
	next := flag.Bool("print-next-version", false, "print next version without building")
	flag.Parse()
	settingsPath := *settings
	if *storage != "" {
		dir, err := os.MkdirTemp("", "gmha-build-settings-*")
		if err != nil {
			log.Fatal(err)
		}
		defer os.RemoveAll(dir)
		settingsPath = filepath.Join(dir, "settings.json")
	}
	packages, err := app.NewPackageService(settingsPath, nil)
	if err != nil {
		log.Fatal(err)
	}
	if *storage != "" {
		if err := packages.SetStoragePath(*storage); err != nil {
			log.Fatal(err)
		}
	}
	if *next {
		v, err := app.NextAgentVersion(packages)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(v)
		return
	}
	root, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	items, err := app.BuildAndPublishAgent(ctx, root, packages)
	if err != nil {
		log.Fatal(err)
	}
	for _, item := range items {
		fmt.Printf("%s %s %s\n", item.Version, item.Arch, item.Name)
	}
}

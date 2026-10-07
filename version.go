package main

import (
	_ "embed"
	"encoding/json"
)

// The desktop resources, updater and release script share the same version.
//
//go:embed wails.json
var applicationMetadata []byte

//go:embed build/repository.json
var repositoryMetadata []byte

func applicationVersion() string {
	var metadata struct {
		Info struct {
			ProductVersion string `json:"productVersion"`
		} `json:"info"`
	}
	if json.Unmarshal(applicationMetadata, &metadata) != nil || metadata.Info.ProductVersion == "" {
		return "0.0.0"
	}
	return metadata.Info.ProductVersion
}

func updateRepository() (owner, repo string) {
	var metadata struct{ Owner, Repo string }
	_ = json.Unmarshal(repositoryMetadata, &metadata)
	return metadata.Owner, metadata.Repo
}

package main

import (
	"os"
	"path/filepath"

	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	configDirectoryVariable = "LANHOP_CONFIG_DIR"
	configDirectoryName     = "lanhop"
	peersFileName           = "peers.json"
)

func configDirectory() (string, error) {
	if directory := os.Getenv(configDirectoryVariable); directory != "" {
		return directory, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, configDirectoryName), nil
}

func openTrusted() (*trust.Store, error) {
	directory, err := configDirectory()
	if err != nil {
		return nil, err
	}
	return trust.Open(filepath.Join(directory, peersFileName))
}

package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/QEStudios/NMOScillatorCompiler/parser/furnace"
	"github.com/davecgh/go-spew/spew"
	"github.com/sqweek/dialog"
)

var logger *log.Logger

func main() {
	logger = log.New(os.Stdout, "", log.Ldate|log.Ltime)

	// Get the current working directory.
	cwd, err := os.Getwd()
	if err != nil {
		logger.Fatalf("failed to get current working directory: %v", err)
	}

	// Get the path of the Furnace text export file.
	path, err := choosePath(cwd, os.Args)
	if err != nil {
		if errors.Is(err, dialog.ErrCancelled) {
			logger.Printf("User cancelled the file dialog")
			os.Exit(1)
		}
		logger.Fatalf("failed to determine file path: %v", err)
	}

	file, err := os.Open(path)
	if err != nil {
		logger.Fatalf("error opening file: %v", err)
	}
	defer file.Close()

	p := furnace.NewParser(file, logger)
	result, err := p.Parse()
	if err != nil {
		logger.Fatalf("parse error: %v", err)
	}
	song := result.Song
	warnings := result.Warnings

	spew.Dump(song)
	if len(warnings) > 0 {
		logger.Printf("Parser returned %d warning(s):\n", len(warnings))
		for _, w := range warnings {
			logger.Printf("warning: %s", w)
		}
	}
}

// choosePath returns the file path either from the command-line args
// or from an interactive file dialog.
func choosePath(cwd string, args []string) (string, error) {
	// If an argument was passed to the program, use it.
	if len(args) > 1 {
		path := args[1]
		if err := validatePath(path); err != nil {
			return "", fmt.Errorf("passed argument is not a valid path: %w", err)
		}
		return path, nil
	}

	// Otherwise open the file dialog.
	path, err := dialog.
		File().
		Title("Open Furnace text export").
		Filter("Furnace text exports (*.txt)", "txt").
		SetStartDir(cwd).
		Load()
	if err != nil {
		// Propagate the error. Caller will check for dialog.ErrCancelled.
		return "", err
	}

	// Check for empty path just in case.
	if path == "" {
		return "", dialog.ErrCancelled
	}
	if err := validatePath(path); err != nil {
		return "", fmt.Errorf("dialog selection invalid: %w", err)
	}
	return path, nil
}

// validatePath performs simple checks to verify if a file exists or not.
func validatePath(p string) error {
	if strings.ToLower(filepath.Ext(p)) != ".txt" {
		return fmt.Errorf("file must have .txt extension")
	}
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("cannot stat file: %w", err)
	}
	return nil
}

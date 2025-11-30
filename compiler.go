package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/sqweek/dialog"
)

var Logger *log.Logger

func main() {
	Logger = log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime)

	// Get the current working directory.
	cwd, err := os.Getwd()
	if err != nil {
		Logger.Fatalf("Failed to get current working directory: %v", err)
	}

	// Get the path of the Furnace text export file.
	path, err := choosePath(cwd, os.Args)
	if err != nil {
		if errors.Is(err, dialog.ErrCancelled) {
			Logger.Printf("User cancelled the file dialog")
			os.Exit(1)
		}
		Logger.Fatalf("Failed to determine file path: %v", err)
	}

	Logger.Printf("Final file path: %v", path)
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
		Logger.Printf("Using file from argument: %v", path)
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
	Logger.Printf("Selected file: %v", path)
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

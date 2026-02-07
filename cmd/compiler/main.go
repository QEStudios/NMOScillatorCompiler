package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/QEStudios/NMOScillatorCompiler/parser/furnace"
	"github.com/spf13/pflag"
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

	var subsongIndices []int
	pflag.IntSliceVarP(&subsongIndices, "subsong", "s", make([]int, 0), "subsong index (0-127)")
	pflag.Parse()

	// Get the path of the Furnace text export file.
	path, err := choosePath(cwd, pflag.Args())
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

	var rom []byte

	// parse whole file into internal Furnace format.
	p := furnace.NewParser(file, logger)
	internalSong, err := p.ParseInternal()
	if err != nil {
		logger.Fatalf("parse error: %v", err)
	}
	if len(internalSong.Warnings) > 0 {
		logger.Println("Warnings produced while parsing file:")
		for _, warning := range internalSong.Warnings {
			logger.Printf("line %d: %v\n", warning.Line, warning.Message)
		}
	}

	if len(subsongIndices) == 0 {
		// If no subsongs are specified, parse all subsongs into a single rom.

		n := len(internalSong.Song.Subsongs)

		logger.Printf("Concatenating %d subsongs", n)

		subsongIndices = make([]int, n) // Allocate space for the indices.

		for i := range n {
			subsongIndices[i] = i
		}
	}

	// Iterate over every subsong index provided and parse/compile them, then combine them into a single rom.
	for _, subsongIndex := range subsongIndices {
		if subsongIndex > 255 {
			logger.Fatalf("subsong index %d out of range", subsongIndex)
		}

		song, err := p.ParseNmos(internalSong, uint8(subsongIndex))
		if err != nil {
			logger.Fatalf("error parsing subsong %d: %v", subsongIndex, err)
		}

		// fmt.Println(song)

		subsongBin, err := song.Compile()
		if err != nil {
			logger.Fatalf("error compiling subsong %d: %v", subsongIndex, err)
		}

		logger.Printf("Subsong %d:\taddress: %d,\tsize: %d bytes", subsongIndex, len(rom), len(subsongBin))

		rom = slices.Concat(rom, subsongBin)
	}

	logger.Printf("Total rom size: %d bytes", len(rom))

	// Write to a .bin file in the same directory as the source file.
	ext := filepath.Ext(path)
	binPath := strings.TrimSuffix(path, ext) + ".bin"
	err = os.WriteFile(binPath, rom, 0o644)
	if err != nil {
		logger.Fatalf("Error writing output file: %v", err)
	}
}

// choosePath returns the file path either from the command-line args
// or from an interactive file dialog.
func choosePath(cwd string, args []string) (string, error) {
	// If an argument was passed to the program, use it.
	if len(args) > 0 {
		path := args[0]
		absPath, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("cannot get absolute path: %w", err)
		}
		if err := validatePath(absPath); err != nil {
			return "", fmt.Errorf("passed argument is not a valid path: %w", err)
		}
		return absPath, nil
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

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("cannot get absolute path: %w", err)
	}

	// Check for empty path just in case.
	if absPath == "" {
		return "", dialog.ErrCancelled
	}
	if err := validatePath(absPath); err != nil {
		return "", fmt.Errorf("dialog selection invalid: %w", err)
	}
	return absPath, nil
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

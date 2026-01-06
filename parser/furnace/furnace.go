package main

import (
	"fmt"
)

// A song composition, which can contain multiple subsongs.
type Song struct {
	Version int     // The version integer of Furnace that exported this song
	Name    string  // The name of the song.
	Author  string  // The author of the song.
	Album   string  // The album the song is a part of.
	Tuning  float32 // The frequency that A4 maps to in this song (usually 440 hz).

	// A slice of sound chips used in the song.
	SoundChips []*SoundChip

	// A slice of subsongs in the song.
	Subsongs []*Subsong
}

// A single SN76489 sound chip configuration.
type SoundChip struct {
	Index int
	// If true, Divides the base clock frequency fed into the chip by 2 (effectively making it run at half speed and lower all notes by an octave).
	ClockDiv bool
}

// A single subsong inside a whole song composition.
type Subsong struct {
	Index    int
	Name     string  // The name of the subsong (can be blank).
	TickRate float32 // The (starting) tick rate of the song.

	// A slice of up to 16 speed values, where the values cycle every tick.
	// The final update speed is calculated as the Tick Rate divided by the Frame Speed.
	Speeds   []int
	TimeBase int // Not sure what this value means, the Furnace code seems to multiply the speeds by this number + 1, so when this is 0 the speeds remain unchanged.

	// A slice of every frame in the subsong.
	Rows []*Row
}

// A row in the (sub)song.
type Row struct {
	Index int
	Notes []*Note
}

type Note int // A single note (stored as a Midi note number).

/*
isValidNoteString Returns true if the given note string is valid, otherwise returns false.

The note string is always 3 characters. If the note string is exactly "...", the given note is empty/blank, however this is still a valid note string.
Otherwise, the first character of the note string should be a capital letter in the range of A-G.
The second character should be:

- '#' if the note is sharp and the octave is >= 0,

- '+' if the note is sharp and the octave is < 0,

- '-' if the note is natural and the octave is >= 0, or

- '_' if the note is natural and the octave is < 0.

The third character is a digit '0'..'7' representing the absolute value of the octave.
Negative octaves (where the second char == '+' or '_') are only allowed when that digit is <= 5.
(The overall octave range is -5 through 7.)
*/
func isValidNoteString(noteString string) bool {
	// Note strings are always 3 characters long.
	if len(noteString) != 3 {
		return false
	}

	// The specific string "..." is an empty/blank note, and is still a valid note string.
	if noteString == "..." {
		return true
	}

	// The first character must be a capital letter in the range A-G.
	first := noteString[0]
	second := noteString[1]
	third := noteString[2]
	if !(first >= 'A' && first <= 'G') {
		return false
	}

	// The third character must be an integer between 0 and 7.
	if third < '0' || third > '7' {
		return false
	}
	absoluteOctave := int(third - '0')

	// The second character must be '#', '+', '-' or '_'.
	// '+' and '_' are only valid if the absolute value of the octave is <= 5.
	switch second {
	case '#', '+', '-', '_':
		// ok
	default:
		return false
	}
	if (second == '+' || second == '_') && absoluteOctave > 5 {
		return false
	}

	return true
}

var noteBase = map[byte]int{
	'C': 0,
	'D': 2,
	'E': 4,
	'F': 5,
	'G': 7,
	'A': 9,
	'B': 11,
}

// parseNote accepts a note string and returns either a pointer to a Note object defining that note, or nil if there is no note.
func parseNote(noteString string) (*Note, error) {
	// Ensure the note string follows the correct format.
	if !isValidNoteString(noteString) {
		return nil, fmt.Errorf("invalid note string: %s", noteString)
	}

	// Check if there is a note at all. "..." is still valid but means there is no note, so nil should be returned.
	if noteString == "..." {
		return nil, nil
	}

	first := noteString[0]
	second := noteString[1]
	third := noteString[2]

	octave := int(third - '0')

	// Accidentals of '+' or '_' indicate a negative octave.
	if second == '+' || second == '_' {
		octave = -octave
	}

	var accidental int
	switch second {
	case '#', '+':
		accidental = 1
	case '-', '_':
		accidental = 0
	default:
		panic(fmt.Sprintf("invalid accidental: %q", second))
	}

	midiNote := (octave+1)*12 + noteBase[first] + accidental
	note := Note(midiNote)

	return &note, nil
}

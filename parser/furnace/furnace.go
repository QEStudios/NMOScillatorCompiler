package furnace

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"

	"github.com/davecgh/go-spew/spew"
)

// A struct to store a range of Furnace version numbers, used for checking version compatibility for the text exports.
type versionRange struct {
	min, max int
}

// A slice containing the compatible versions of Furnace text exports that this parser can handle.
var supportedRanges = []versionRange{
	{232, 232},
}

// isVersionSupported checks if the given Furnace version number is supported by this parser, and returns true if it is, else it returns false.
func isVersionSupported(version int) bool {
	for _, r := range supportedRanges {
		if version >= r.min && version <= r.max {
			return true
		}
	}
	return false
}

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

type Note struct {
	Pitch  NotePitch
	Volume NoteVolume
	Off    bool // if true, is a note-off
}

type NotePitch int    // A single note (stored as a Midi note number).
type NoteVolume uint8 // A single note's volume (4-bit).

/*
isValidNoteString Returns true if the given note string is valid, otherwise returns false.

The note string is always 3 characters. If the note string is exactly "...", the given note is empty/blank, however this is still a valid note string.
If the note string is exactly "OFF", the note is a note-off.
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

	if noteString == "OFF" {
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

// parseNote accepts a note string and returns either a pointer to a Note struct defining that note, or nil if there is no note.
func parseNote(noteString string) (*Note, error) {
	// Ensure the note string follows the correct format.
	if !isValidNoteString(noteString) {
		return nil, fmt.Errorf("invalid note string: %s", noteString)
	}

	// Check if there is a note at all. "..." is still valid but means there is no note, so nil should be returned.
	if noteString == "..." {
		return nil, nil
	}

	if noteString == "OFF" {
		return &Note{
			Pitch: 0,
			Off:   true,
		}, nil
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

	return &Note{
		Pitch: NotePitch(midiNote),
		Off:   false,
	}, nil
}

// A key and a value, used for key-value list elements.
type listElement struct {
	key   string
	value string
}

// Small struct for non-fatal warnings
type ParseWarning struct {
	Line    int
	Message string
}

func (pi ParseWarning) String() string {
	return fmt.Sprintf("line %d: %s", pi.Line, pi.Message)
}

type Parser struct {
	scanner    *bufio.Scanner
	logger     *log.Logger
	lineNumber int
	state      string
	song       Song

	// Collect any warnings whilst parsing.
	warnings []ParseWarning

	// Generic per-state context storage.
	stateCtx map[string]any

	// Whether or not the parser has already been used.
	// Parsing can only be done once per Parser.
	used bool
}

type ParseResult struct {
	Song     *Song
	Warnings []ParseWarning
}

// NewParser creates a new parser to parse a file.
func NewParser(r io.Reader, logger *log.Logger) *Parser {
	if logger == nil {
		logger = log.Default()
	}
	song := Song{
		Version: 0,
		Name:    "Unnamed",
		Author:  "Unknown",
		Album:   "",
		Tuning:  440,
	}
	return &Parser{
		scanner:  bufio.NewScanner(r),
		logger:   logger,
		state:    "signature", // Parser starts looking for the signature initially.
		song:     song,
		stateCtx: make(map[string]any),
	}
}

// addWarning adds to the list of warnings encountered when parsing.
func (p *Parser) addWarning(format string, args ...any) {
	p.warnings = append(p.warnings, ParseWarning{
		Line:    p.lineNumber,
		Message: fmt.Sprintf(format, args...),
	})
}

func (p *Parser) fatalf(format string, args ...any) error {
	return fmt.Errorf("line %d: %s", p.lineNumber, fmt.Sprintf(format, args...))
}

// Parses a line containing a list element into a ListElement struct.
func parseListElement(s string) (*listElement, error) {
	idx := strings.Index(s, ":")
	if idx == -1 {
		return nil, fmt.Errorf("invalid list element: %s", s)
	}

	key := strings.TrimSpace(s[:idx])
	value := strings.TrimSpace(s[idx+1:])

	key, found := strings.CutPrefix(key, "- ")
	if !found {
		return nil, fmt.Errorf("invalid list element: %s", s)
	}

	return &listElement{key: key, value: value}, nil
}

// parseSpeedsList parses a string containing 1..16 positive non-zero integers
// separated by whitespace. It returns a slice of pointers to each parsed int ([]*int).
func (p *Parser) parseSpeedsList(s string) ([]int, error) {
	tokens := strings.Fields(s)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("expected 1..16 numbers, got none")
	}

	if len(tokens) > 16 {
		p.addWarning("speeds list contains %d numbers, only first 16 will be used", len(tokens))
	}

	count := len(tokens)
	if count > 16 {
		count = 16
	}

	out := make([]int, 0, count)
	for i := 0; i < count; i++ {
		token := tokens[i]
		v, err := strconv.Atoi(token)
		if err != nil {
			return nil, fmt.Errorf("token %d (%q) in speeds list is not a valid integer: %w", i+1, token, err)
		}
		if v <= 0 {
			return nil, fmt.Errorf("token %d (%q) in speeds list must be positive and non-zero", i+1, token)
		}

		out = append(out, v)
	}

	return out, nil
}

// setState saves an arbitrary value for a given state name.
func (p *Parser) setState(name string, v any) {
	p.stateCtx[name] = v
}

// getState returns the stored value for name and whether it existed.
// Usage: st, ok := getState[*SeenState](p, "song information")
func getState[T any](p *Parser, name string) (T, bool) {
	var zero T
	v, ok := p.stateCtx[name]
	if !ok {
		return zero, false
	}
	typed, ok := v.(T)
	if !ok {
		return zero, false
	}
	return typed, true
}

func (p *Parser) getCurrentChip() *SoundChip {
	if len(p.song.SoundChips) == 0 {
		return nil
	}
	return p.song.SoundChips[len(p.song.SoundChips)-1]
}

func (p *Parser) getCurrentSubsong() *Subsong {
	if len(p.song.Subsongs) == 0 {
		return nil
	}
	return p.song.Subsongs[len(p.song.Subsongs)-1]
}

type boolMap struct {
	Ctx map[string]bool
}

func (p *Parser) parseInternal() (*ParseResult, error) {
	if p.used {
		return nil, fmt.Errorf("parser already used")
	}
	p.used = true
	for p.scanner.Scan() {
		p.lineNumber++
		line := p.scanner.Text()
		trimmedLine := strings.TrimSpace(line)

		// p.logger.Printf("Line %04d: %s", p.lineNumber, line)

		// Blank lines are always ignored regardless of location in the file.
		if trimmedLine == "" {
			continue
		}

		switch p.state {
		// The very top of the file where the Furnace signature "# Furnace Text Export" is found.
		case "signature":
			if trimmedLine == "# Furnace Text Export" {
				p.state = "version"
				continue
			}
			p.addWarning("unexpected text found in file when looking for Furnace signature: %s", trimmedLine)

		// Right under the Furnace signature, the Furnace version number should be present.
		case "version":
			if strings.HasPrefix(trimmedLine, "generated by Furnace ") {
				parts := strings.Fields(trimmedLine)
				last := parts[len(parts)-1] // Should be the version integer.
				numStr := strings.Trim(last, "()")
				version, err := strconv.Atoi(numStr)

				if err != nil {
					return nil, p.fatalf("invalid integer found in Furnace version number: %s", numStr)
				}

				if !isVersionSupported(version) {
					p.addWarning("Furnace version number %d isn't officially supported by this program. some things might not work correctly", version)
				}

				p.song.Version = version
				p.logger.Printf("Furnace version %d detected\n", version)

				p.setState("song information", &boolMap{
					Ctx: map[string]bool{
						"name":   false,
						"author": false,
						"tuning": false,
					},
				})

				p.state = "song information"
				continue
			}
			return nil, p.fatalf("unexpected text found in file when looking for Furnace version: %s", trimmedLine)

		case "song information":
			if trimmedLine == "# Song Information" { // Section header.
				continue
			}

			if trimmedLine == "# Sound Chips" { // Next section, check that we've seen everything we need to.
				st, ok := getState[*boolMap](p, "song information")
				if !ok {
					return nil, p.fatalf("internal error: song info state missing")
				}
				var missing []string
				for key, seen := range st.Ctx {
					if !seen {
						missing = append(missing, key)
					}
				}

				if len(missing) > 0 {
					return nil, p.fatalf("missing fields in Song Information section: %s", strings.Join(missing, ", "))
				}

				p.setState("sound chips", &boolMap{
					Ctx: map[string]bool{
						"parsingChip":  false, // Should be set to true if we are in the middle of parsing a chip
						"parsingFlags": false, // Should be set to true if we are in the middle of parsing chip flags
						"id":           false,
						"flags":        false,
						"chipType":     false,
						"customClock":  false,
					},
				})

				p.state = "sound chips"
				continue
			}

			le, err := parseListElement(trimmedLine)
			if err != nil {
				return nil, p.fatalf("error parsing list element when extracting song information: %s", trimmedLine)
			}

			st, _ := getState[*boolMap](p, "song information")
			switch le.key {
			case "name":
				p.song.Name = le.value
				st.Ctx["name"] = true
			case "author":
				p.song.Author = le.value
				st.Ctx["author"] = true
			case "album":
				p.song.Album = le.value
			case "tuning":
				tuning, err := strconv.ParseFloat(le.value, 32)
				if err != nil {
					return nil, p.fatalf("error converting song tuning in text file to a number: %s", le.value)
				}
				p.song.Tuning = float32(tuning)
				st.Ctx["tuning"] = true
			case "system", "instruments", "wavetables", "samples":
				// Ignore; not important.
			default:
				p.addWarning("unknown option in Song Information section: %s", le.key)
			}

		case "sound chips":
			if trimmedLine == "# Sound Chips" { // Section header.
				continue
			}

			st, _ := getState[*boolMap](p, "sound chips")

			if trimmedLine == "# Instruments" { // Next section, check that we've seen everything we need to.
				if st.Ctx["parsingFlags"] == true {
					p.addWarning("didn't finish parsing chip properly in Sound Chips section. This could be because there were no flags present on a chip")
				}

				if st.Ctx["parsingChip"] {
					var missing []string
					for key, seen := range st.Ctx {
						if key == "parsingChip" || key == "parsingFlags" {
							continue // Ignore the parsing chip and parsing flags states
						}
						if !seen {
							missing = append(missing, key)
						}
						st.Ctx[key] = false // Reset seen flag
					}

					if len(missing) > 0 {
						return nil, p.fatalf("missing fields in Sound Chips section: %s", strings.Join(missing, ", "))
					}
				}

				if len(p.song.SoundChips) == 0 {
					return nil, p.fatalf("no sound chips were found by the parser")
				}

				p.state = "instruments/wavetables/samples"
				continue
			} else if st.Ctx["parsingFlags"] {
				if trimmedLine == "```" {
					st.Ctx["parsingFlags"] = false
					continue
				}
				kv := strings.SplitN(trimmedLine, "=", 2)
				if len(kv) != 2 {
					return nil, p.fatalf("invalid chip flag: %s", trimmedLine)
				}
				key := strings.TrimSpace(kv[0])
				value := strings.TrimSpace(kv[1])

				chipPtr := p.getCurrentChip()
				if chipPtr == nil {
					return nil, fmt.Errorf("internal error: parsingFlags true but no current chip")
				}

				switch key {
				case "chipType":
					if value != "4" {
						return nil, p.fatalf("chip type for chip number %d was expected to be TI SN76489A (chip id 4), instead found chip id %s.", len(p.song.SoundChips), value)
					}
					st.Ctx["chipType"] = true
				case "customClock":
					switch value {
					case "4000000":
						chipPtr.ClockDiv = false
					case "2000000":
						chipPtr.ClockDiv = true
					default:
						p.addWarning("custom clock for chip number %d should be either 4000000 (4 MHz) or 2000000 (2 MHz) due to hardware limitations. Defaulting to 4 MHz", len(p.song.SoundChips))
					}
					st.Ctx["customClock"] = true
				case "clockSel", "noEasyNoise", "noPhaseReset":
					// Ignore; not important.
				default:
					p.addWarning("unknown chip flag in Sound Chips section: %s", key)
				}
				continue
			} else {
				if trimmedLine == "- TI SN76489" {
					if st.Ctx["parsingChip"] == true {
						if st.Ctx["parsingFlags"] == true {
							p.addWarning("didn't finish parsing chip properly in Sound Chips section. This could be because there were no flags present on a chip")
						}
						var missing []string
						for key, seen := range st.Ctx {
							if key == "parsingChip" || key == "parsingFlags" {
								continue // Ignore the parsing chip and parsing flags states
							}
							if !seen {
								missing = append(missing, key)
							}
							st.Ctx[key] = false // Reset seen flag
						}

						if len(missing) > 0 {
							return nil, p.fatalf("missing fields in Sound Chips section: %s", strings.Join(missing, ", "))
						}
						// Fall through to start a new chip
					}

					st.Ctx["parsingChip"] = true
					st.Ctx["parsingFlags"] = false
					p.song.SoundChips = append(p.song.SoundChips, &SoundChip{Index: len(p.song.SoundChips)})
					continue
				} else if trimmedLine == "```" {
					st.Ctx["parsingFlags"] = true
					continue
				}
				le, err := parseListElement(trimmedLine)
				if err != nil {
					return nil, p.fatalf("error parsing list element when extracting sound chips: %s", trimmedLine)
				}

				chipPtr := p.getCurrentChip()
				if chipPtr == nil {
					return nil, p.fatalf("no current chip while parsing")
				}

				switch le.key {
				case "id":
					st.Ctx["id"] = true
					if le.value != "04" {
						return nil, p.fatalf("expected chip id 04 at line %d in Sound Chips section, found id %s instead. Make sure you choose 'TI SN76489' as the sound chip in Furnace", p.lineNumber, le.key)
					}
				case "flags":
					st.Ctx["flags"] = true
				case "volume", "panning", "front/rear":
					// Ignore; not important.
				default:
					p.addWarning("unknown option in Sound Chips section at line %d: %s", p.lineNumber, le.key)
				}
			}

		case "instruments/wavetables/samples":
			if trimmedLine == "# Instruments" || trimmedLine == "# Wavetables" || trimmedLine == "# Samples" { // Section headers to ignore.
				continue
			}
			if trimmedLine == "# Subsongs" {
				p.setState("subsongs", &boolMap{
					Ctx: map[string]bool{
						"parsingSubsong":  false,
						"parsingMetadata": false,
						"parsingOrders":   false,
						"parsingRows":     false,
						"tickRate":        false,
						"speeds":          false,
					},
				})

				p.state = "subsongs"
				continue
			}

		case "subsongs":

			if trimmedLine == "# Subsongs" { // Section header
				continue
			}

			st, _ := getState[*boolMap](p, "subsongs")

			if st.Ctx["parsingRows"] {
				// TODO
			}

			var subsongName string
			newIdx := len(p.song.Subsongs)
			if strings.HasPrefix(trimmedLine, "## ") {
				if trimmedLine == "## Patterns" {
					if st.Ctx["parsingOrders"] == true {
						st.Ctx["parsingOrders"] = false
						st.Ctx["parsingRows"] = true
					}
					// Do nothing else because we don't care about orders.
					continue
				}

				validLine := true

				for { // Scope to break out of if the syntax isn't valid.
					splitIdx := strings.Index(trimmedLine, ":")
					if splitIdx == -1 {
						validLine = false
						break
					}

					key := strings.TrimSpace(trimmedLine[:splitIdx])
					subsongName = strings.TrimSpace(trimmedLine[splitIdx+1:])

					var found bool
					key, found = strings.CutPrefix(key, "## ")
					if !found {
						validLine = false
						break
					}

					claimedIdx, err := strconv.Atoi(key)
					if err != nil {
						validLine = false
						break
					}
					if claimedIdx != newIdx { // Make sure the subsong index is what we expect.
						p.addWarning("expected subsong index %d, got index %d instead", newIdx, claimedIdx)
					}

					break
				}

				if validLine == false {
					p.addWarning("unexpected text found in file when looking for subsong start: %s", trimmedLine)
					continue
				}

				if st.Ctx["parsingSubsong"] == st.Ctx["parsingRows"] {
					if st.Ctx["parsingSubsong"] == true {
						if st.Ctx["parsingMetadata"] == true {
							return nil, p.fatalf("didn't finish parsing subsong metadata properly in Sound Chips section")
						}
						if st.Ctx["parsingOrders"] == true {
							return nil, p.fatalf("didn't finish parsing subsong orders properly in Sound Chips section")
						}

						var missing []string
						for key, seen := range st.Ctx {
							if key == "parsingSubsong" || key == "parsingMetadata" || key == "parsingOrders" {
								continue // Ignore the parsing subsong state.
							}
							if !seen {
								missing = append(missing, key)
							}
							st.Ctx[key] = false // Reset seen flag.
						}

						if len(missing) > 0 {
							return nil, p.fatalf("missing fields in Subsongs section: %s", strings.Join(missing, ", "))
						}
						// Fall through to start a new subsong.
					}

					st.Ctx["parsingSubsong"] = true
					st.Ctx["parsingMetadata"] = true
					st.Ctx["parsingOrders"] = false
					st.Ctx["parsingRows"] = false

					p.song.Subsongs = append(p.song.Subsongs, &Subsong{
						Index:    newIdx,
						Name:     subsongName,
						TickRate: 50,
						Speeds:   []int{3},
					})
					continue
				} else {
					p.addWarning("unexpected text found in file when parsing subsong id %d: %s", newIdx-1, trimmedLine)
				}
				continue
			}

			if st.Ctx["parsingMetadata"] {
				if trimmedLine == "orders:" {
					st.Ctx["parsingMetadata"] = false
					st.Ctx["parsingOrders"] = true
					continue
				}

				le, err := parseListElement(trimmedLine)
				if err != nil {
					return nil, p.fatalf("error parsing list element when extracting sound chips: %s", trimmedLine)
				}

				subsongPtr := p.getCurrentSubsong()
				if subsongPtr == nil {
					return nil, p.fatalf("no current subsong while parsing")
				}

				switch le.key {
				case "tick rate":
					st.Ctx["tickRate"] = true
					tickRate, err := strconv.ParseFloat(le.value, 32)
					if err != nil {
						return nil, p.fatalf("error converting song tick rate in text file to a number: %s", le.value)
					}
					subsongPtr.TickRate = float32(tickRate)
				case "speeds":
					st.Ctx["speeds"] = true
					speeds, err := p.parseSpeedsList(le.value)
					if err != nil {
						return nil, p.fatalf("error when parsing speeds: %v", err)
					}
					subsongPtr.Speeds = speeds
				case "time base":
					timeBase, err := strconv.Atoi(le.value)
					if err != nil {
						return nil, p.fatalf("error converting song time base in text file to a number: %s", le.value)
					}
					subsongPtr.TimeBase = timeBase
				case "virtual tempo", "pattern length":
					// Ignore; not important.
				default:
					p.addWarning("unknown option in Sound Chips section: %s", le.key)
				}
			}

		default:
			spew.Dump(p.song)
			return nil, p.fatalf("unknown parser state: %s", p.state)
		}

	}

	if err := p.scanner.Err(); err != nil {
		p.logger.Fatalf("error while reading file: %v", err)
	}

	fileComplete := false
	if p.state == "subsongs" {
		st, _ := getState[*boolMap](p, "subsongs")
		if st.Ctx["parsingSubsong"] == true && st.Ctx["parsingRows"] == true {
			// This should mean we've finished parsing the file and it wasn't cut off at the end.
			// Not the most rigorous check because the song could totally have no notes in it,
			// but we can check for that elsewhere in the code.
			fileComplete = true
		}
	}
	if !fileComplete {
		return nil, p.fatalf("unexpected EOF")
	}

	return &ParseResult{
		Song:     &p.song,
		Warnings: p.warnings,
	}, nil
}

func (p *Parser) Parse() (*ParseResult, error) {
	return p.parseInternal()
}

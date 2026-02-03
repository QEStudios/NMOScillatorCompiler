package furnace

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/QEStudios/NMOScillatorCompiler/nmos"
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
	Tuning  float64 // The frequency that A4 maps to in this song (usually 440 hz).

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
	Index         int
	Name          string  // The name of the subsong (can be blank).
	TickRate      float64 // The (starting) tick rate of the song.
	PatternLength uint8   // The length of each pattern in the song.

	// A slice of up to 16 speed values, where the values cycle every tick.
	// The final update speed is calculated as the Tick Rate divided by the Frame Speed.
	Speeds   []uint8
	TimeBase int // Not sure what this value means, the Furnace code seems to multiply the speeds by this number + 1, so when this is 0 the speeds remain unchanged.

	// A slice of every frame in the subsong.
	Rows []Row
}

// A row in the (sub)song.
type Row struct {
	Index   int
	Notes   []Note
	Effects []Effect
}

type Note struct {
	Pitch    NotePitch
	HasPitch bool

	Volume    NoteVolume
	HasVolume bool

	Off bool // if true, is a note-off

	Channel Channel
}

type Channel uint8
type NotePitch int    // A single note (stored as a Midi note number).
type NoteVolume uint8 // A single note's volume (4-bit).
type EffectType int

// pitchToFreq converts a Midi note number to a frequency, given a specific tuning of A4.
func pitchToFreq(pitch NotePitch, tuning float64) float64 {
	// For some reason, furnace notates the octaves as being two octaves *lower* than what they really sound like.
	// So we need to offset it by bumping the note pitch up two octaves before converting.
	offsetPitch := pitch + 24
	return tuning * math.Pow(2, float64(offsetPitch-69)/12)
}

const (
	EffectJumpToPattern EffectType = iota
	EffectJumpToNextPattern
	EffectSpeed
	EffectNoiseControl
	EffectTickRateHz
	EffectTickRateBpm
	EffectStopSong
)

type Effect struct {
	Type  EffectType
	Value uint16
}

/*
isValidPitchString returns true if the given pitch string is valid, otherwise returns false.

The pitch string is always 3 characters.
The first character of the pitch string should be a capital letter in the range of A-G.
The second character should be:

- '#' if the pitch is sharp and the octave is >= 0,

- '+' if the pitch is sharp and the octave is < 0,

- '-' if the pitch is natural and the octave is >= 0, or

- '_' if the pitch is natural and the octave is < 0.

The third character is a digit '0'..'7' representing the absolute value of the octave.
Negative octaves (where the second char == '+' or '_') are only allowed when that digit is <= 5.
(The overall octave range is -5 through 7.)
*/
func isValidPitchString(pitchString string) bool {
	// Pitch strings are always 3 characters long.
	if len(pitchString) != 3 {
		return false
	}

	upperString := strings.ToUpper(pitchString)
	first := upperString[0]
	second := upperString[1]
	third := upperString[2]

	// The first character must be a capital letter in the range A-G.
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

// parsePitchString parses a pitch string and returns a NotePitch.
func parsePitchString(pitchString string) (NotePitch, error) {
	// Ensure the pitch string follows the correct format.
	if !isValidPitchString(pitchString) {
		return NotePitch(0), fmt.Errorf("invalid pitch string '%s'", pitchString)
	}

	upperString := strings.ToUpper(pitchString)
	first := upperString[0]
	second := upperString[1]
	third := upperString[2]

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
		panic(fmt.Sprintf("invalid accidental '%q'", second))
	}

	midiNote := (octave+1)*12 + noteBase[first] + accidental
	return NotePitch(midiNote), nil
}

// isValidVolumeString returns true if the given volume string is of a valid format.
// This means either .. for no change, or a hex number 00 through 0F.
func isValidVolumeString(volumeString string) bool {
	// Volume strings are always 3 characters long.
	if len(volumeString) != 2 {
		return false
	}

	if volumeString[0] != '0' {
		// There's no other option this could be apart from a volume value.
		// Only volume values 00 through 0F are valid, so if it starts with anything else it's invalid.
		return false
	}

	if volumeString[1] >= '0' && volumeString[1] <= '9' {
		return true
	}
	upperString := strings.ToUpper(volumeString)
	if upperString[1] >= 'A' && upperString[1] <= 'F' {
		return true
	}
	return false
}

// parseVolumeString parses a volume string and returns a NoteVolume.
func parseVolumeString(volumeString string) (NoteVolume, error) {
	// Ensure the pitch string follows the correct format.
	if !isValidVolumeString(volumeString) {
		return NoteVolume(0), fmt.Errorf("invalid volume string '%s'", volumeString)
	}

	volume, err := strconv.ParseUint(volumeString, 16, 4)
	if err != nil {
		return NoteVolume(0), fmt.Errorf("error parsing volume string: %w", err)
	}

	return NoteVolume(volume), nil
}

// isValidEffectString returns true if the given effect string is of a valid format.
// This means either .... for no change, or a hex number 0000 through FFFF.
func isValidEffectString(effectString string) bool {
	// Effect strings are always 4 characters long.
	if len(effectString) != 4 {
		return false
	}

	_, err := strconv.ParseUint(effectString[0:2], 16, 8)
	if err != nil {
		return false
	}
	if effectString[2:4] != ".." {
		_, err := strconv.ParseUint(effectString[2:4], 16, 8)
		if err != nil {
			return false
		}
	}

	return true
}

// parseEffectString parses an effect string and returns an Effect struct.
func parseEffectString(effectString string) (Effect, error) {
	// Ensure the effect string follows the correct format.
	if !isValidEffectString(effectString) {
		return Effect{}, fmt.Errorf("invalid effect string '%s'", effectString)
	}

	effectId, err := strconv.ParseUint(effectString[0:2], 16, 8)
	if err != nil {
		return Effect{}, fmt.Errorf("error parsing effect string: %w", err)
	}

	var effectType EffectType
	var value uint64

	if effectId >= 0xC0 && effectId <= 0xCF {
		// Set tick rate (hz) effect is all effects 0xC0 through 0xCF,
		// as the value is 12 bit and rolls over into the first byte.
		effectType = EffectTickRateHz
		if effectString[2:4] == ".." {
			value, err = strconv.ParseUint(string(effectString[1]), 16, 4)
			value <<= 8
		} else {
			value, err = strconv.ParseUint(effectString[1:4], 16, 16)
		}
		if err != nil {
			return Effect{}, fmt.Errorf("error parsing effect string: %w", err)
		}
	} else {
		switch effectId {
		case 0x0B:
			effectType = EffectJumpToPattern
		case 0x0D:
			effectType = EffectJumpToNextPattern
		case 0x09, 0x0F:
			effectType = EffectSpeed
		case 0x20:
			effectType = EffectNoiseControl
		case 0xF0:
			effectType = EffectTickRateBpm
		case 0xFF:
			effectType = EffectStopSong
		default:
			// Error if we find any unrecognised effects.
			return Effect{}, fmt.Errorf("unrecognised effect '%s'", effectString)
		}

		if effectString[2:4] == ".." {
			value = 0
		} else {
			value, err = strconv.ParseUint(effectString[2:4], 16, 8)
			if err != nil {
				return Effect{}, fmt.Errorf("error parsing effect string: %w", err)
			}
		}
	}

	return Effect{Type: effectType, Value: uint16(value)}, nil
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

// parseNote accepts a note string, which is a combination of a pitch, instrument (ignored), volume,
// and any number of effects, and returns a Note struct defining that note (or nil if there is no note),
// a slice of effects (which may contain no effects), and an error if something went wrong.
func parseNote(noteString string) (Note, []Effect, error) {

	// Remove any whitespace
	cleanedNoteString := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, noteString)

	// Make sure note strings are a valid length.
	// 3 (pitch) + 2 (instrument) + 2 (volume) + 4 for every effect (minimum 1 effect).
	if (len(cleanedNoteString)-11)%4 != 0 {
		return Note{}, nil, fmt.Errorf("invalid note string: %s", noteString)
	}

	pitchString := cleanedNoteString[0:3]
	volumeString := cleanedNoteString[5:7]

	var err error

	var pitch NotePitch
	var volume NoteVolume
	hasPitch := true
	hasVolume := true
	off := false

	switch pitchString {
	case "...":
		pitch = NotePitch(0)
		hasPitch = false
	case "OFF":
		pitch = NotePitch(0)
		hasPitch = false
		volume = NoteVolume(0)
		hasVolume = false
		off = true
	default:
		pitch, err = parsePitchString(pitchString)
		if err != nil {
			return Note{}, nil, err
		}
	}

	if !off { // Make sure that the volume value hasn't been set to 0 already by a note OFF.
		switch volumeString {
		case "..":
			volume = NoteVolume(0)
			hasVolume = false
		default:
			volume, err = parseVolumeString(volumeString)
			if err != nil {
				return Note{}, nil, err
			}
		}
	}

	var effects []Effect

	for i := 0; i < len(cleanedNoteString)-7; i += 4 {
		effectString := cleanedNoteString[i+7 : i+11]
		if effectString == "...." {
			// Don't store empty effects.
			continue
		}
		effect, err := parseEffectString(effectString)
		if err != nil {
			return Note{}, nil, err
		}
		effects = append(effects, effect)
	}

	return Note{
		Pitch:     NotePitch(pitch),
		HasPitch:  hasPitch,
		Volume:    volume,
		HasVolume: hasVolume,
		Off:       off,
	}, effects, nil
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
// separated by whitespace. It returns a slice of each parsed int ([]int).
func (p *Parser) parseSpeedsList(s string) ([]uint8, error) {
	tokens := strings.Fields(s)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("expected 1..16 numbers, got none")
	}

	// TODO
	if len(tokens) > 1 {
		return nil, fmt.Errorf("compiler doesn't currently support groove patterns")
	}

	if len(tokens) > 16 {
		p.addWarning("speeds list contains %d numbers, only first 16 will be used", len(tokens))
	}

	count := min(16, len(tokens))

	out := make([]uint8, 0, count)
	for i := 0; i < count; i++ {
		token := tokens[i]
		v, err := strconv.Atoi(token)
		if err != nil {
			return nil, fmt.Errorf("token %d (%q) in speeds list is not a valid integer: %w", i+1, token, err)
		}
		if v <= 0 || v >= 256 {
			return nil, fmt.Errorf("token %d (%q) in speeds list must be in the range 1..255", i+1, token)
		}

		out = append(out, uint8(v))
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
				p.logger.Printf("Furnace version %d detected", version)

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
				tuning, err := strconv.ParseFloat(le.value, 64)
				if err != nil {
					return nil, p.fatalf("error converting song tuning in text file to a number: %s", le.value)
				}
				p.song.Tuning = tuning
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
						"patternLength":   false,
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
				if strings.HasPrefix(trimmedLine, "----- ORDER") { // Order header
					continue
				}
				fields := strings.FieldsFunc(trimmedLine, func(r rune) bool {
					return r == '|'
				})

				subsongPtr := p.getCurrentSubsong()
				if subsongPtr == nil {
					return nil, p.fatalf("no current subsong while parsing")
				}
				row := Row{
					Index: len(subsongPtr.Rows),
				}

				for i, field := range fields {
					if i == 0 { // Ignore address values.
						continue
					}

					note, effects, err := parseNote(field)
					if err != nil {
						p.addWarning("error parsing note in channel %d: %v", i-1, err)
						row.Notes = append(row.Notes, Note{Channel: Channel(i - 1)})
						continue
					}
					note.Channel = Channel(i - 1)

					row.Notes = append(row.Notes, note)
					row.Effects = append(row.Effects, effects...)
				}

				subsongPtr.Rows = append(subsongPtr.Rows, row)
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
						Speeds:   []uint8{3},
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
					tickRate, err := strconv.ParseFloat(le.value, 64)
					if err != nil {
						return nil, p.fatalf("error converting song tick rate in text file to a number: %s", le.value)
					}
					subsongPtr.TickRate = tickRate
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
				case "pattern length":
					st.Ctx["patternLength"] = true
					patternLength, err := strconv.ParseUint(le.value, 10, 8)
					if err != nil {
						return nil, p.fatalf("error convert pattern length in text file to a number: %s", le.value)
					}
					subsongPtr.PatternLength = uint8(patternLength)
				case "virtual tempo":
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
		p.fatalf("error while reading file: %v", err)
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

type noiseRateTypeEnum int

const (
	noiseRateCh3 noiseRateTypeEnum = iota
	noiseRatePreset
)

func (p *Parser) parseNmos(result *ParseResult, subsongIndex uint8) (*nmos.NmosSong, error) {
	parsedSong := result.Song
	song := nmos.NmosSong{}
	if subsongIndex >= uint8(len(parsedSong.Subsongs)) {
		return nil, p.fatalf("subsong %d does not exist; song only contains %d subsongs (allowed range 0..%d)",
			subsongIndex,
			len(parsedSong.Subsongs), len(parsedSong.Subsongs)-1,
		)
	}
	subsong := parsedSong.Subsongs[subsongIndex]

	p.logger.Printf("Will parse %d rows", len(subsong.Rows))

	if len(parsedSong.SoundChips) > 1 {
		p.logger.Printf("Found %d sound chips, output will use the first one", len(parsedSong.SoundChips))
	}

	// Currently only one sound chip exists on the NMOScillator, so just assume the first sound chip is the one to use.
	const soundchipIndex = 0

	song.Name = ""
	if parsedSong.Name != "" {
		song.Name += parsedSong.Name
		if subsong.Name != "" {
			song.Name += " - "
		}
	}
	if subsong.Name != "" {
		song.Name += subsong.Name
	}
	if parsedSong.Album != "" {
		song.Name += fmt.Sprintf(" (from %s)", parsedSong.Album)
	}
	song.Author = parsedSong.Author

	finalTickrate := subsong.TickRate / (float64(subsong.Speeds[0]) * float64(subsong.TimeBase+1))

	tempo, baseFrameDelay, _, _, ok := nmos.FindBestRate(finalTickrate)

	if !ok {
		return nil, fmt.Errorf("unable to find compatible tickrate within an acceptable tolerance")
	}

	song.InitialTempo = tempo
	song.ClockDiv = parsedSong.SoundChips[soundchipIndex].ClockDiv

	var clockRate float64
	if song.ClockDiv {
		clockRate = 2_000_000
	} else {
		clockRate = 4_000_000
	}

	if clockRate == 2_000_000 {
		// NMOScillator doesn't currently support using the ClockDiv option.
		return nil, fmt.Errorf("Clock rate of 2 MHz is not currently supported by the NMOScillator")
	}

	var noiseRateType noiseRateTypeEnum
	var noiseMode nmos.NoiseMode
	var currentSpeed uint8
	var currentTickRate float64
	var loopTargetIndex int

	currentSpeed = subsong.Speeds[0]
	currentTickRate = subsong.TickRate

	var isHalted bool // Does the song now halt? (used for breaking out of the loop)
	var isLooped bool // Does the song now loop back to an earlier point? (used for breaking out of the loop)

	resetFrame := nmos.Frame{}
	err := resetFrame.SetNoiseControl(nmos.WhiteNoise, nmos.Channel3Noise)
	if err != nil {
		return nil, fmt.Errorf("error generating reset frame: %v", err)
	}

	for c := 0; c < 4; c++ {
		resetFrame.SetAttenuation(uint8(c), 0xf)
	}

	song.Frames = append(song.Frames, resetFrame)

	channelVolumes := []uint8{0xf, 0xf, 0xf, 0xf}
	channelOffs := []bool{true, true, true, true} // Slice of 4 bools for whether each channel is off (true) or not (false).

	for rowIndex := 0; rowIndex < len(subsong.Rows); {
		newIndex := rowIndex + 1
		row := subsong.Rows[rowIndex]

		frame := nmos.Frame{}

		isBlank := true

		// Effects
		for _, effect := range row.Effects {
			switch effect.Type {
			case EffectJumpToPattern:
				currentPattern := rowIndex / int(subsong.PatternLength)
				if int(effect.Value) > currentPattern { // skip forward
					newIndex = int(effect.Value) * int(subsong.PatternLength)
					p.logger.Printf("Jump to row %d", newIndex)
				} else { // loop backward
					loopTargetIndex = int(effect.Value)*int(subsong.PatternLength) + 1
					song.LoopTarget = loopTargetIndex
					isLooped = true
					isBlank = false
				}

			case EffectJumpToNextPattern:
				currentPattern := rowIndex / int(subsong.PatternLength)
				newIndex = (currentPattern + 1) * int(subsong.PatternLength)
				p.logger.Printf("Jump to row %d", newIndex)

			case EffectSpeed:
				if len(subsong.Speeds) > 1 {
					p.logger.Println("changing speed patterns using set groove pattern / set speed effects is not supported yet, ignoring")
				} else {
					finalTickrate := currentTickRate / (float64(effect.Value) * float64(subsong.TimeBase+1))
					tempo, newBaseFrameDelay, _, _, ok := nmos.FindBestRate(finalTickrate)
					if !ok {
						return nil, fmt.Errorf("unable to find compatible tickrate within an acceptable tolerance")
					}
					baseFrameDelay = newBaseFrameDelay

					err := frame.SetNewTempo(tempo)
					if err != nil {
						return nil, fmt.Errorf("error setting frame tempo: %v", err)
					}
					currentSpeed = uint8(effect.Value)
					isBlank = false
				}

			case EffectNoiseControl:
				rateVal := effect.Value >> 4
				modeVal := effect.Value % 16

				if rateVal == 1 {
					noiseRateType = noiseRateCh3
				} else {
					noiseRateType = noiseRatePreset
				}

				if modeVal == 1 {
					noiseMode = nmos.WhiteNoise
				} else {
					noiseMode = nmos.PeriodicNoise
				}

				if noiseRateType == noiseRateCh3 {
					err := frame.SetNoiseControl(noiseMode, nmos.Channel3Noise)
					if err != nil {
						return nil, fmt.Errorf("error setting noise control values: %v", err)
					}
				}
				// If not ch3 noise, noise uses preset frequency, and this means the noise control should be updated
				// only when changing the preset (with a note pitch set in the noise channel).
				isBlank = false

			case EffectTickRateHz:
				finalTickrate := float64(effect.Value) / (float64(currentSpeed) * float64(subsong.TimeBase+1))
				tempo, newBaseFrameDelay, closestRate, relErr, ok := nmos.FindBestRate(finalTickrate)
				if !ok {
					return nil, fmt.Errorf("unable to find compatible tickrate within an acceptable tolerance.")
				}
				baseFrameDelay = newBaseFrameDelay
				// DEBUG
				p.logger.Printf("New speed: %d", effect.Value)
				p.logger.Printf("Target tick rate: %0.2f. Chosen tempo: %d, base frame delay: %d, closest rate: %0.3f, error: %0.4f", finalTickrate, tempo, baseFrameDelay, closestRate, relErr)

				err := frame.SetNewTempo(tempo)
				if err != nil {
					return nil, fmt.Errorf("error setting frame tempo: %v", err)
				}
				currentTickRate = float64(effect.Value)
				isBlank = false

			case EffectTickRateBpm:
				tickRateHz := float64(effect.Value) * 24 / 60 // Furnace assumes 24 ticks per beat, I had to figure this out the hard way.
				finalTickrate := tickRateHz / (float64(currentSpeed) * float64(subsong.TimeBase+1))
				tempo, newBaseFrameDelay, closestRate, relErr, ok := nmos.FindBestRate(finalTickrate)
				if !ok {
					return nil, fmt.Errorf("unable to find compatible tickrate within an acceptable tolerance")
				}
				baseFrameDelay = newBaseFrameDelay
				// DEBUG
				p.logger.Printf("New speed: %d", effect.Value)
				p.logger.Printf("Target tick rate: %0.2f. Chosen tempo: %d, base frame delay: %d, closest rate: %0.3f, error: %0.4f", finalTickrate, tempo, baseFrameDelay, closestRate, relErr)

				err := frame.SetNewTempo(tempo)
				if err != nil {
					return nil, fmt.Errorf("error setting frame tempo: %v", err)
				}
				currentTickRate = tickRateHz
				isBlank = false

			case EffectStopSong:
				// We can stop parsing the song after this frame.
				// Since the NMOScillator has no way of actually halting the playback,
				// we instead send it into an infinite loop at the end of the song.
				isHalted = true
				isBlank = false

			default:
				panic(fmt.Sprintf("unknown effect type %d", effect.Type))
			}
		}

		frame.FrameDelay = baseFrameDelay

		// Notes
		for _, note := range row.Notes {

			if note.Off {
				err := frame.SetAttenuation(uint8(note.Channel), 0xf)
				if err != nil {
					return nil, fmt.Errorf("error setting channel off: %v", err)
				}
				channelOffs[note.Channel] = true
				isBlank = false
			}

			if note.HasVolume {
				vol := uint8(note.Volume)
				if !channelOffs[note.Channel] {
					err := frame.SetAttenuation(uint8(note.Channel), 0xf-vol)
					if err != nil {
						return nil, fmt.Errorf("error setting channel attenuation off: %v", err)
					}
				}
				channelVolumes[note.Channel] = vol
				isBlank = false
			}

			if note.HasPitch && note.Channel < 3 { // Set pitch for square channels.
				period := nmos.CalculateSquarePeriod(pitchToFreq(note.Pitch, parsedSong.Tuning), clockRate)
				err := frame.SetSquarePeriod(uint8(note.Channel), period)
				if err != nil {
					return nil, fmt.Errorf("error setting channel period: %v", err)
				}
				if channelOffs[note.Channel] {
					err := frame.SetAttenuation(uint8(note.Channel), 0xf-channelVolumes[note.Channel])
					if err != nil {
						return nil, fmt.Errorf("error setting channel on: %v", err)
					}
					channelOffs[note.Channel] = false
				}
				isBlank = false
			} else if note.HasPitch && note.Channel == 3 { // Set pitch for noise channel
				if noiseRateType == noiseRateCh3 {
					// TODO: Maybe noise channel in pulse mode isn't the right frequency,
					// and should be calculated differently? I remember it being slightly
					// off in pitch.
					period := nmos.CalculateSquarePeriod(pitchToFreq(note.Pitch, parsedSong.Tuning), clockRate)
					err := frame.SetSquarePeriod(2, period)
					if err != nil {
						return nil, fmt.Errorf("error setting noise period: %v", err)
					}
					if channelOffs[3] {
						err := frame.SetAttenuation(3, 0xf-channelVolumes[3])
						if err != nil {
							return nil, fmt.Errorf("error setting noise attenuation: %v", err)
						}
						channelOffs[3] = false
					}
				} else {
					// Noise mode is set to preset, so C = LOW, C# = MED, and D = HIGH.

					var preset nmos.NoiseRate
					switch note.Pitch % 12 { // Check the note pitch regardless of octave (C, C#, D, etc).
					case 0: // C
						preset = nmos.LowNoise
					case 1: // C#
						preset = nmos.MediumNoise
					case 2: // D
						preset = nmos.HighNoise
					default: // any other pitch
						return nil, fmt.Errorf("unable to convert noise pitch %d into a noise mode preset", note.Pitch)
					}

					err := frame.SetNoiseControl(noiseMode, preset)
					if err != nil {
						return nil, fmt.Errorf("error setting noise control values: %v", err)
					}
					if channelOffs[3] {
						err := frame.SetAttenuation(3, 0xf-channelVolumes[3])
						if err != nil {
							return nil, fmt.Errorf("error setting noise attenuation: %v", err)
						}
						channelOffs[3] = false
					}
				}
				isBlank = false
			}
		}

		rowIndex = newIndex

		// If this frame will be empty, increase the frame delay of the previous frame
		// instead of making a new frame. Only make a new frame if the previous frame's delay can't get higher.
		if isBlank {
			prevFrame := &song.Frames[len(song.Frames)-1]

			// HACK: will probably break when adding groove support.
			if int(prevFrame.FrameDelay)+int(baseFrameDelay) <= 255 { // Frame delay can be increased.
				prevFrame.FrameDelay += baseFrameDelay
				continue // Don't append this blank frame.
			}
		}

		if isHalted { // Break out of the loop early if we encountered a halt frame.
			loopTargetIndex = len(song.Frames)
			song.LoopTarget = loopTargetIndex
			song.Frames = append(song.Frames, frame)

			haltFrame := nmos.Frame{
				LoopToTarget: true,
			}
			song.Frames = append(song.Frames, haltFrame)
			break
		}

		// TODO: groove patterns

		song.Frames = append(song.Frames, frame)

		if isLooped { // Finish parsing if the song will loop forever from this point.
			song.Frames = append(song.Frames, frame)

			haltFrame := nmos.Frame{
				LoopToTarget: true,
			}
			song.Frames = append(song.Frames, haltFrame)
			break
		}
	}

	if !(isHalted || isLooped) {
		// Song has no loop or halt effects, so default to looping back to the start (this is what furnace does).

		loopFrame := nmos.Frame{
			LoopToTarget: true,
		}
		song.Frames = append(song.Frames, loopFrame)
		song.LoopTarget = 0 // This should be the default value regardless but I like being explicit.
	}

	return &song, nil
}

// Parses into an NmosSong struct.
func (p *Parser) Parse(subsongIndex uint8) (*nmos.NmosSong, error) {
	internalSong, err := p.parseInternal()
	if err != nil {
		return nil, err
	}

	if len(internalSong.Warnings) > 0 {
		p.logger.Println("Warnings produced while parsing file:")
		for _, warning := range internalSong.Warnings {
			p.logger.Printf("line %d: %v\n", warning.Line, warning.Message)
		}
	}

	return p.parseNmos(internalSong, subsongIndex)
}

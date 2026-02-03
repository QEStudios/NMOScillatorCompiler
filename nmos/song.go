package nmos

import (
	"fmt"
	"math"
	"strings"
)

const maxSquarePeriod = (1 << 10) - 1
const maxAttenuation = (1 << 4) - 1
const maxTempo = (1 << 7) - 1

type CommandType int

const (
	SetSquarePeriodCommand CommandType = iota // Set the period of a square channel.
	SetAttenuationCommand                     // Set the attenuation of a channel (including noise).
	SetNoiseControlCommand                    // Set the noise channel operation.
)

// No need for an isValid for CommandType as it's only assigned internally by helper functions.

type NoiseMode int

const (
	PeriodicNoise NoiseMode = iota
	WhiteNoise
)

func (m NoiseMode) isValid() bool {
	switch m {
	case PeriodicNoise, WhiteNoise:
		return true
	default:
		return false
	}
}

type NoiseRate int

const (
	LowNoise NoiseRate = iota
	MediumNoise
	HighNoise
	Channel3Noise
)

func (r NoiseRate) isValid() bool {
	switch r {
	case LowNoise, MediumNoise, HighNoise, Channel3Noise:
		return true
	default:
		return false
	}
}

// A single song composition. Multiple of these could be loaded onto a single ROM at a time, if desired.
type NmosSong struct {
	Name   string // Name of the song.
	Author string // Author of the song.

	InitialTempo uint8 // Initial tempo of the song.
	// If true, Divides the base clock frequency fed into the chip by 2
	// (effectively making it run at half speed and lower all notes by an octave).
	ClockDiv   bool
	Frames     []Frame
	LoopTarget int // The index of the frame which will be marked as the Loop Target.
}

// A single frame in a song.
type Frame struct {
	commands   []command // Slice of SN76489 commands.
	FrameDelay uint8     // Frame Delay byte.

	hasTempoChange bool  // Whether or not this frame should update the value of the Tempo Register.
	tempo          uint8 // If HasTempoChange is true, this is the new tempo used after this frame (7-bit).

	LoopToTarget bool // Whether the song should loop back to the Loop Target at this frame.
}

// An SN76489 command.
type command struct {
	commandType CommandType // What type of SN76489 command this command is.

	channel     uint8     // For Types SetSquarePeriod, SetAttenuation: The channel to which this command applies (2-bit).
	period      uint16    // For Type SetSquarePeriod: The period to set the square channel to (10-bit).
	attenuation uint8     // For Type SetAttenuation: The attenuation to set the channel to (4-bit).
	noiseMode   NoiseMode // For Type SetNoiseControl: The Noise Mode that the noise channel should use.
	noiseRate   NoiseRate // For Type SetNoiseControl: The Noise Rate that the noise channel should use.
}

func (c *command) String() string {
	switch c.commandType {
	case SetSquarePeriodCommand:
		return fmt.Sprintf("Set period to %d", c.period)

	case SetAttenuationCommand:
		return fmt.Sprintf("Set atten. to %d", c.attenuation)

	case SetNoiseControlCommand:
		var mode string
		var rate string
		switch c.noiseMode {
		case WhiteNoise:
			mode = "white"
		case PeriodicNoise:
			mode = "pulse"
		}
		switch c.noiseRate {
		case LowNoise:
			rate = "low"
		case MediumNoise:
			rate = "med"
		case HighNoise:
			rate = "high"
		case Channel3Noise:
			rate = "ch3"
		}
		return fmt.Sprintf("Mode: %s %s", mode, rate)
	default:
		return ""
	}
}

// SetNewTempo makes the frame change the tempo of the song when it is played.
// Multiple calls of this method to the same frame will return an error.
func (f *Frame) SetNewTempo(tempo uint8) error {
	if f.hasTempoChange == true {
		return fmt.Errorf("frame already has a tempo change")
	}
	if tempo > maxTempo {
		return fmt.Errorf("tempo must be 0-%d, got %d", maxTempo, tempo)
	}

	f.tempo = tempo
	f.hasTempoChange = true
	return nil
}

// commandAlreadyExists returns whether a command setting a specific value already exists.
func (f *Frame) commandAlreadyExists(commandType CommandType, channel uint8) bool {
	// Technically a nonsensical channel number could be passed here, but I don't really care. It's an internal helper.
	for _, cmd := range f.commands { // O(n), but that doesn't matter because n is below 16 anyway. No reason to over-optimise.
		if cmd.commandType == commandType && (cmd.commandType == SetNoiseControlCommand || cmd.channel == channel) {
			// Noise Control commands take no channel argument, no need to check their channel (we know it's the noise channel)
			return true
		}
	}
	return false
}

// SetSquarePeriod adds a command to the frame setting the period of a square wave channel.
// Multiple calls setting the period of the same channel in the same frame will return an error.
func (f *Frame) SetSquarePeriod(channel uint8, period uint16) error {
	if channel > 2 {
		return fmt.Errorf("square channel must be 0-2, got %d", channel)
	}
	if period > maxSquarePeriod {
		return fmt.Errorf("square period must be 0-%d, got %d", maxSquarePeriod, period)
	}
	if f.commandAlreadyExists(SetSquarePeriodCommand, channel) {
		return fmt.Errorf("square period already set for channel %d in this frame", channel)
	}

	f.commands = append(f.commands, command{
		commandType: SetSquarePeriodCommand,
		channel:     channel,
		period:      period,
	})
	return nil
}

// SetAttenuation adds a command to the frame setting the attenuation of a channel (including noise).
// Multiple calls setting the period of the same channel in the same frame will return an error.
// Note that "attenuation" and "volume" are different. Attenuation is the inverse of volume, such that
// 0xf attenuation will be silent and 0x0 attenuation is full volume.
func (f *Frame) SetAttenuation(channel uint8, attenuation uint8) error {
	if channel > 3 {
		return fmt.Errorf("square channel must be 0-3, got %d", channel)
	}
	if attenuation > maxAttenuation {
		return fmt.Errorf("attenuation must be 0-%d, got %d", maxAttenuation, attenuation)
	}
	if f.commandAlreadyExists(SetAttenuationCommand, channel) {
		return fmt.Errorf("attenuation already set for channel %d in this frame", channel)
	}

	f.commands = append(f.commands, command{
		commandType: SetAttenuationCommand,
		channel:     channel,
		attenuation: attenuation,
	})
	return nil
}

func (f *Frame) SetNoiseControl(mode NoiseMode, rate NoiseRate) error {
	if !mode.isValid() {
		return fmt.Errorf("invalid noise mode: %d", mode)
	}
	if !rate.isValid() {
		return fmt.Errorf("invalid noise rate: %d", rate)
	}
	// Pass channel 0 for the check, ignored anyway because it's a noise channel command
	if f.commandAlreadyExists(SetNoiseControlCommand, 0) {
		return fmt.Errorf("noise control already set in this frame")
	}

	f.commands = append(f.commands, command{
		channel:     3,
		commandType: SetNoiseControlCommand,
		noiseMode:   mode,
		noiseRate:   rate,
	})
	return nil
}

// formatCommandsByChannel formats channel into a table with numChannels columns.
// commands: input slice
// numChannels: number of channels/columns to print.
// headerNames: optional names for each channel (if nil or empty entry, "Channel i" is used).
// indent: number of spaces to indent the table
func formatCommandsByChannel(commands []command, numChannels int, headerNames []string, indent int) string {
	if numChannels <= 0 {
		numChannels = 4
	}

	// Group by channel
	cols := make([][]command, numChannels)
	for _, c := range commands {
		if int(c.channel) >= numChannels {
			// Out-of-range channel, ignore
			continue
		}
		cols[c.channel] = append(cols[c.channel], c)
	}

	// Find max rows
	maxRows := 0
	for _, col := range cols {
		if len(col) > maxRows {
			maxRows = len(col)
		}
	}

	// Calculate column widths
	widths := make([]int, numChannels)
	for i := range numChannels {
		// Header
		header := fmt.Sprintf("Channel %d", i)
		if i < len(headerNames) && headerNames[i] != "" {
			header = headerNames[i]
		}
		widths[i] = len(header)

		// Cells
		for _, cmd := range cols[i] {
			s := cmd.String()
			if len(s) > widths[i] {
				widths[i] = len(s)
			}
		}

		// Set a minimum width for nicer output
		widths[i] = max(widths[i], 18)
	}

	// Helper padding functions
	padRight := func(s string, w int) string {
		if len(s) >= w {
			return s
		}
		return s + strings.Repeat(" ", w-len(s))
	}

	var b strings.Builder

	// Top separator
	b.WriteString(strings.Repeat(" ", indent))
	for i := range numChannels {
		b.WriteString("+")
		b.WriteString(strings.Repeat("-", widths[i]+2))
	}
	b.WriteString("+\n")

	// Header row
	b.WriteString(strings.Repeat(" ", indent))
	for i := range numChannels {
		header := fmt.Sprintf("Channel %d", i)
		if i < len(headerNames) && headerNames[i] != "" {
			header = headerNames[i]
		}
		b.WriteString("| ")
		b.WriteString(padRight(header, widths[i]))
		b.WriteString(" ")
	}
	b.WriteString("|\n")

	// Separator row
	b.WriteString(strings.Repeat(" ", indent))
	for i := 0; i < numChannels; i++ {
		b.WriteString("+")
		b.WriteString(strings.Repeat("-", widths[i]+2)) // +2 for the space padding either side
	}
	b.WriteString("+\n")

	// Command rows
	for row := range maxRows {
		b.WriteString(strings.Repeat(" ", indent))
		for channel := range numChannels {
			cell := ""
			if row < len(cols[channel]) {
				cell = cols[channel][row].String()
			}
			b.WriteString("| ")
			b.WriteString(padRight(cell, widths[channel]))
			b.WriteString(" ")
		}
		b.WriteString("|\n")
	}

	// Final separator
	b.WriteString(strings.Repeat(" ", indent))
	for i := range numChannels {
		b.WriteString("+")
		b.WriteString(strings.Repeat("-", widths[i]+2))
	}
	b.WriteString("+\n")

	return b.String()
}

// Pretty-print
func (s *NmosSong) String() string {
	var b strings.Builder
	b.WriteString("NMOScillator Song:\n")
	fmt.Fprintf(&b, "- Name: %s\n", s.Name)
	fmt.Fprintf(&b, "- Author: %s\n", s.Author)
	fmt.Fprintf(&b, "- Initial tempo: %d\n", s.InitialTempo)
	b.WriteString("- Clock rate: ")
	if s.ClockDiv {
		b.WriteString("2 MHz\n")
	} else {
		b.WriteString("4 MHz\n")
	}

	b.WriteString("- Frames:\n")

	for i, frame := range s.Frames {
		fmt.Fprintf(&b, "\n  - Frame #%d:", i)

		if s.LoopTarget == i { // If this frame is the loop target
			b.WriteString(" (loop target)")
		}
		b.WriteString("\n")

		if i == 0 { // First frame logic.
			// First frame in the song should set the song's tempo.
			// Because the data format stores the initial tempo separately,
			// we have to set the frame's tempo here so it's calculated accurately.
			// HACK: If the frame already has a tempo which would override the initial tempo,
			// this command will error anyway. We can ignore this command's errors
			frame.SetNewTempo(s.InitialTempo)
		}

		if len(frame.commands) > 0 {
			headers := []string{
				"Square 1",
				"Square 2",
				"Square 3",
				"Noise",
			}
			table := formatCommandsByChannel(frame.commands, 4, headers, 6)
			b.WriteString(table)
		}

		if frame.hasTempoChange {
			fmt.Fprintf(&b, "    - Change tempo to %d (0x%x)\n", frame.tempo, frame.tempo)
		}
		fmt.Fprintf(&b, "    - Frame delay: %d\n", frame.FrameDelay)
		if frame.LoopToTarget {
			fmt.Fprintf(&b, "    - Loop to target (frame #%d)\n", s.LoopTarget)
		}

		frameSize := frame.CalculateSize()
		fmt.Fprintf(&b, "    [Total length: %d byte", frameSize)
		if frameSize != 1 {
			b.WriteString("s") // Pluralise the word "byte" if needed.
		}
		b.WriteString("]\n")
	}

	totalSize := s.CalculateSize()
	fmt.Fprintf(&b, "[Total song size: %d byte", totalSize)
	if totalSize != 1 {
		b.WriteString("s") // Pluralise the word "byte" if needed.
	}
	b.WriteString("]\n")

	return b.String()
}

// effectiveTickRate calculates the effective tick rate for a given Tempo Register value (0..127)
// and Frame Delay (0..255).
func effectiveTickRate(tempo uint8, frameDelay uint8) float64 {
	return 31250 / ((float64(frameDelay) + 1) * (float64(tempo) + 129))
}

// bestTempoForDelay calculates the tempo (0..127) that best approximates the targetRate
// for a fixed frameDelay. It returns the tempo, the achieved tick rate and the relative error.
func bestTempoForDelay(targetRate float64, frameDelay uint8) (bestTempo uint8, bestAchieved float64, bestErr float64) {
	// Ideal float tempo.
	ideal := 31250/((float64(frameDelay)+1)*targetRate) - 129

	// Consider the nearest integer tempos.
	candidates := []uint8{
		uint8(math.Floor(ideal)),
		uint8(math.Ceil(ideal)),
	}

	bestErr = math.Inf(1)
	bestTempo = 0
	bestAchieved = 0.0

	for _, tempo := range candidates {
		tempo = min(127, tempo)

		achieved := effectiveTickRate(tempo, frameDelay)
		relErr := math.Abs(achieved-targetRate) / targetRate
		if relErr < bestErr {
			bestErr = relErr
			bestTempo = tempo
			bestAchieved = achieved
		}
	}

	return bestTempo, bestAchieved, bestErr
}

// 1.0% tolerance.
const maxRateError float64 = 0.01

// FindBestRate searched frameDelay values in ascending order and returns
// the smallest frameDelay for which some tempo yields relative error <= maxRelError.
// If maxRelError <= 0 the function returns ok=false immediately (invalid tolerance).
// If no combination meets the tolerance, ok=false.
func FindBestRate(targetRate float64) (tempo uint8, frameDelay uint8, achieved float64, relErr float64, ok bool) {
	if maxRateError <= 0 {
		return 0, 0, 0, 0, false // Return ok=false
	}

	for fd := range 255 {
		t, a, rel := bestTempoForDelay(targetRate, uint8(fd))
		if rel <= maxRateError {
			return t, uint8(fd), a, rel, true
		}
	}
	return 0, 0, 0, 0, false // Return ok=false
}

// CalculateSquarePeriod computes the (rounded) period of a square channel from a given frequency and clock rate.
func CalculateSquarePeriod(freq float64, clockRate float64) uint16 {
	return uint16(math.RoundToEven(clockRate / (32 * freq)))
}

// CalculateNoisePeriod computes the (rounded) period of the noise channel from a given frequency and clock rate.
func CalculateNoisePeriod(freq float64, clockRate float64) uint16 {
	return uint16(math.RoundToEven(clockRate / (30 * freq)))
}

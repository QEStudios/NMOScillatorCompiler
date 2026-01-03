package main

// A song composition, which can contain multiple subsongs.
type Song struct {
	Name   string  // The name of the song.
	Author string  // The author of the song.
	Album  string  // The album the song is a part of.
	Tuning float32 // The frequency that A4 maps to in this song (usually 440 hz).

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
	Speeds   []*int
	TimeBase uint8 // Not sure what this value means, the Furnace code seems to multiply the speeds by this number + 1, so when this is 0 the speeds remain unchanged.

	// A slice of every frame in the subsong.
	Rows []*Row
}

// A row in the (sub)song.
type Row struct {
	Index int
	Notes []*Note
}

type Note int // A single note (stored as a Midi note number).

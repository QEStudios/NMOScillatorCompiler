package nmos

import (
	"bytes"
	"fmt"
)

// CalculateSize returns the size in bytes of the frame.
func (f *Frame) CalculateSize() int {
	if f.hasTempoChange {
		// Tempo changes require the frame to be 15+ bytes long.
		// This is the only way a 15+ byte frame can exist.
		return 15
	}

	runningTotal := 1 // Frame header data takes 1 byte
	for _, command := range f.commands {
		if command.commandType == SetSquarePeriodCommand {
			// Period commands on the square wave channel are 2 bytes long.
			runningTotal += 2
		} else {
			// All other commands are 1 byte long.
			runningTotal++
		}
	}

	if runningTotal > 1 || f.FrameDelay > 0 {
		// Frame delay byte is required when there are commands.
		// The only time a frame delay byte may be omitted is when the frame
		// contains no commands and has no frame delay value.
		// This is essentially a blank frame which just waits a tick.
		runningTotal++
	}

	return runningTotal
}

// CalculateSize returns the total size in bytes of the song.
func (s *NmosSong) CalculateSize() int {
	size := 0
	for i, frame := range s.Frames {
		frameSize := frame.CalculateSize()
		if i == 0 {
			// Initial tempo is an extra byte in the first frame when compiling,
			// but only if the first frame doesn't already have a tempo set.
			if !frame.hasTempoChange {
				// Frames with a tempo change are always 15 bytes long.
				// Thus, the first frame in the song must be 15 bytes long.
				frameSize = 15
			}
		}
		size += frameSize
	}
	return size
}

// toBytes converts the command into a slice of bytes which should be written to ROM in order to execute this command.
func (c *command) toBytes() []byte {
	// Descriptions of the data formats used by the SN76489 can be found in the SN76489 Apprilcation Manual.
	// They are not described in comments here because it would take too long.
	switch c.commandType {
	case SetSquarePeriodCommand:
		// Period commands on the square wave channel are 2 bytes long.
		output := []byte{0, 0}

		output[0] = 0b10000000                     // MSB=1
		output[0] |= (c.channel & 0b00000111) << 5 // Next 2 bits are the channel.
		output[0] |= byte(c.period & 0b00001111)   // Lowest 4 bits are the 4 LSB of the period.

		output[1] = byte((c.period >> 4) & 0b00111111)

		return output

	case SetAttenuationCommand:
		// Attenuation commands are 1 byte long.
		output := []byte{0}

		output[0] = 0b10010000                     // MSB=1
		output[0] |= (c.channel & 0b00000111) << 5 // Next 2 bits are the channel.
		output[0] |= c.attenuation & 0b00001111    // Lowest 4 bits are the attenuation value.

		return output

	case SetNoiseControlCommand:
		// Noise control commands are 1 byte long.
		output := []byte{0}

		output[0] = 0b10000000                     // MSB=1
		output[0] |= (c.channel & 0b00000111) << 5 // Next 2 bits are the channel.

		var mode int
		switch c.noiseMode {
		case PeriodicNoise:
			mode = 0
		case WhiteNoise:
			mode = 1
		}

		var rate int
		switch c.noiseRate {
		case HighNoise:
			rate = 0
		case MediumNoise:
			rate = 1
		case LowNoise:
			rate = 2
		case Channel3Noise:
			rate = 3
		}

		output[0] |= byte((mode & 0b00000001) << 2) // 3rd LSB is the noise mode bit.
		output[0] |= byte(rate & 0b00000011)        // Lowest two bits are the noise rate bits.

		return output

	default:
		panic(fmt.Sprintf("unhandled command type %d", c.commandType))
	}
}

// Compile converts the song data into the ROM binary format that the NMOScillator can play.
func (s *NmosSong) Compile() ([]byte, error) {
	totalSize := s.CalculateSize()
	buffer := bytes.NewBuffer(make([]byte, 0, totalSize))

	for i, frame := range s.Frames {

		if i == 0 { // First frame logic.
			// If the first frame has a tempo change, we don't want to also output the InitialTempo to it.
			// This function will fail if the frame already has a tempo change, so we can let it check.
			frame.SetNewTempo(s.InitialTempo)
			// HACK: We can ignore any errors (probably not the best idea though).
		}

		frameSize := frame.CalculateSize()
		numCommands := frameSize&0x0f - 1

		// Calculate the number of command bytes we actually care about writing to the frame (so #commands - #dummy commands)
		commandBytesToWrite := 0
		for _, command := range frame.commands {
			if command.commandType == SetSquarePeriodCommand {
				// Period commands on the square wave channel are 2 bytes long.
				commandBytesToWrite += 2
			} else {
				// All other commands are 1 byte long.
				commandBytesToWrite++
			}
		}
		if commandBytesToWrite > 1 || frame.FrameDelay > 0 {
			commandBytesToWrite++
		}

		const (
			flagLoopTarget   = 1 << 7 // 0b10000000
			flagLoopToTarget = 1 << 6 // 0b01000000
		)

		var header byte
		header = 0b00000000

		if i == s.LoopTarget {
			// If this frame is the loop target, set the appropriate flag bit.
			header |= flagLoopTarget
		}
		if frame.LoopToTarget {
			// If this frame should cause a loop back to the target, set the appropriate flag bit.
			header |= flagLoopToTarget
		}

		// Set the lowest 4 bits to the number of commands in the frame (-1 to account for the size of the header).
		header |= byte(numCommands)

		buffer.WriteByte(header)

		// Store the last command written to the frame, to be used as a dummy command if needed.
		var lastCommand byte

		// The reason we iterate over a range instead of frame.commands is because
		// the number of command bytes required may not be the number of actual commands we want to execute.
		// This happens when the frame contains a tempo change, as tempo changes are always
		// at command index 14, and so we need filler "dummy commands" to make the index go that high.
		chipCommandIndex := 0
		c := numCommands
		for c > 0 {
			// fmt.Println("")
			if c == 14 {
				// Tempo change command.
				if frame.hasTempoChange {
					// Only write the first 7 bits, which is the highest the tempo should be anyway.
					buffer.WriteByte(frame.tempo & 0x7f)
				} else {
					// If the frame doesn't have a tempo value set (for some reason),
					// make it re-set the tempo value to be the same as the current value (so it doesn't change the tempo).

				}
				c--
				continue
			}

			if c == 1 {
				// Frame delay command.
				buffer.WriteByte(frame.FrameDelay)
				c--
				continue
			}

			// The formula here checks if we've already written every command we need to,
			// and thus outputs true if we should write a dummy command to pad out the frame.
			// fmt.Printf("Frame's command length: %d\n", numCommands)
			// fmt.Printf("Number of actual commands: %d\n", commandBytesToWrite)
			isChipCommand := (c > 1 && c < 14)
			// fmt.Printf("Command index: %d\n", c)
			// fmt.Printf("Chip command index: %d\n", chipCommandIndex)
			isDummyCommand := chipCommandIndex >= len(frame.commands)
			// fmt.Printf("Is dummy command: %t\n", isDummyCommand)
			if isChipCommand && isDummyCommand {
				// If the command index is higher than the number of commands we want to execute,
				// and the command index specifies a sound chip command, fill the index with a dummy command.
				// In this case, the dummy command is the last command repeated again,
				// which hopefully shouldn't cause audible artifacts, but also changes nothing about
				// the way the chip is running. Essentially performing no operation.
				buffer.WriteByte(lastCommand)
				c--
				continue
			}

			// Write all other chip commands now.
			if isChipCommand {
				// fmt.Printf("Handling command '%s'\n", frame.commands[chipCommandIndex].String())
				commandBytes := frame.commands[chipCommandIndex].toBytes()
				if len(commandBytes) == 0 {
					panic(fmt.Sprintf("command is 0 bytes long! Command trying to parse: '%s'", frame.commands[chipCommandIndex].String()))
				}
				buffer.Write(commandBytes)
				// fmt.Printf("Command byte length: %d\n", len(commandBytes))
				lastCommand = commandBytes[len(commandBytes)-1]
				c -= len(commandBytes)
				chipCommandIndex++
				continue
			}

			panic(fmt.Sprintf("encountered command index %d", c))
		}

		// TODO: continue writing compile logic
	}

	// Sanity check to make sure the output binary is the expected size.
	if buffer.Len() != totalSize {
		return nil, fmt.Errorf("ROM image size mismatch: got %d bytes, expected %d", buffer.Len(), totalSize)
	}
	return buffer.Bytes(), nil
}

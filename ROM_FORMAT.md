## Data Format

Every song is made up of a series of 'frames'. Each frame contains data which is either sent to the SN76489 chip, or used to control the NMOScillator throughout the song. For example, a frame may set the Tempo Register value to 7, and tell the SN76489 to play the note G4 on channel 0 at max volume.

Frames are played sequentially in ROM, and the speed at which they are played is controlled by the internal Tempo Register. The [Tempo and Timing Control](#tempo-and-timing-control) section details how to change the Tempo Register.

There are no other forms of data stored in music ROMs, frames contain all the information needed to control the NMOScillator and to play music.

The first byte in every frame follows the format `TLxxNNNN`:
- When bit `T` (Loop Target) is set, that frame will be set as the Loop Target.
- When bit `L` (Loop) is set, the song will loop back to the Loop Target immediately. Nothing else in the frame will be executed, and the next frame (the Loop Target) will wait to be played.
- Bits `xx` are reserved and are ignored by the NMOScillator.
- The nibble `NNNN` specifies the number of commands present in the frame (this will be referred to as N).

Following this is a series of N bytes, called 'commands'. The 'command index' is initialised to N for the first command byte, and counts down to 1 (for example, a frame with N=14 means commands arrive with indices 14, 13, ..., 2, 1.) The meaning of a command byte is dependant on its command index: If the command index is between 2 and 13 inclusive, the command is streamed directly to the SN76489. Otherwise (if the index is 1, 14, or 15), the command is treated as an *NMOScillator Command* and is interpreted as follows:

- **Index 1 - Frame Delay**:  
  The byte's value specifies how many additional Frame Clock cycles the current frame should take. The NMOScillator's internal Address Counter will not advance for this number of cycles. A frame with N=0 contains no command bytes, and therefore cannot include a Frame Delay command. See the [Tempo and Timing Control](#tempo-and-timing-control) section for more information on frame timings.

- **Index 14 - Tempo Change**:  
  The lower 7 bits of this byte are copied into the Tempo Register. This will affect the tempo of the song as described in the [Tempo and Timing Control](#tempo-and-timing-control) section.

- **Index 15 - UNUSED**:  
  This byte behaves identically to index 14, however it will always be overwritten by the byte at index 14, so it serves no purpose.

## Example Frames

Some example frames are provided here to aid your understanding of the format.

<details>
<summary>Example Frame 1 (click to expand)</summary>

### Example Frame 1

Below is an example of a 5-byte frame which does the following:
- Marks itself as the loop target
- Sets channel 0's 'frequency' to 200 (SN76489 command)
- Sets channel 0's attenuation to 0, AKA max volume (SN76489 command)
- Delays reading the next frame by 3 extra Frame Clock cycles

|    Byte    |  Type                | Description                                                                               |
|:----------:|:--------------------:|:------------------------------------------------------------------------------------------|
| `10000100` | Header               | Set frame as Loop Target. The frame contains 4 command bytes.                             |
| `10001000` | SN76489 Command      | (command index 4) First byte of two-byte message to set the channel 0 'frequency' to 200. |
| `00001100` | SN76489 Command      | (command index 3) Second byte of the channel 0 frequency message.                         |
| `10010000` | SN76489 Command      | (command index 2) Set the channel 0 attenuation to 0, AKA max volume.                     |
| `00000011` | NMOScillator Command | (command index 1) Delay reading the next frame by 3 extra Frame Clock cycles.             |

</details>

<details>
<summary>Example Frame 2 (click to expand)</summary>

### Example Frame 2

Below is an example of a 15-byte frame which does the following:
- Marks itself as the loop target
- Sets the tempo to a value of 12
- Sends 12 commands to the SN76489 to play a note on every channel (including the noise channel) at max volume
- Delays reading the next frame by 16 extra Frame Clock cycles

|    Byte    |  Type                | Description                                                                                |
|:----------:|:--------------------:|:-------------------------------------------------------------------------------------------|
| `10001110` | Header               | Set frame as Loop Target. The frame contains 14 command bytes.                             |
| `00001100` | NMOScillator Command | (command index 14) Set tempo to 12                                                         |
| `10001000` | SN76489 Command      | (command index 13) First byte of two-byte message to set the channel 0 'frequency' to 200. |
| `00001100` | SN76489 Command      | (command index 12) Second byte of the channel 0 frequency message.                         |
| `10010000` | SN76489 Command      | (command index 11) Set the channel 0 attenuation to 0, AKA max volume.                     |
| `10100000` | SN76489 Command      | (command index 10) First byte of two-byte message to set the channel 1 'frequency' to 400. |
| `00011001` | SN76489 Command      | (command index 9) Second byte of the channel 1 frequency message.                          |
| `10110100` | SN76489 Command      | (command index 8) Set the channel 1 attenuation to 4.                                      |
| `11001000` | SN76489 Command      | (command index 7) First byte of two-byte message to set the channel 2 'frequency' to 600.  |
| `00100101` | SN76489 Command      | (command index 6) Second byte of the channel 2 frequency message.                          |
| `11011000` | SN76489 Command      | (command index 5) Set the channel 2 attenuation to 8.                                      |
| `11100110` | SN76489 Command      | (command index 4) Set noise mode to 'white' and set period to HIGH.                        |
| `11110000` | SN76489 Command      | (command index 3) Set the noise channel attenuation to 0, AKA max volume.                  |
| `11110000` | SN76489 Command      | (command index 2) Set the noise channel attenuation to 0, AKA max volume (filler command). |
| `00010000` | NMOScillator Command | (command index 1) Delay reading the next frame by 16 extra Frame Clock cycles.             |

</details>

## Tempo and Timing Control


Internally, the NMOScillator has a programmable frequency divider, called the Frame Clock, chained with a fixed frequency divide-by-128 stage which divides the base clock frequency (usually 4 MHz). The Frame Clock controls the speed at which frames are played. Songs can control the speed of the Frame Clock by writing to the Tempo Register as described in the [Data Format](#data-format) section

Additionally, frames may contain a Frame Delay command byte which defers playing the next frame for $D$ cycles of the Frame Clock (where $D$ is the Frame Delay value). In this way, every frame may take a different amount of time to advance (that is, frames implicitly have variable duration). They are not forced to align with any timing unit by default. This allows for better use of the ROM space, as well as for additional tempo control. For example, if every frame has a Frame Delay of 1, then each frame would only play on every *second* cycle of the Frame Clock, thus halving the effective tempo of the song.

In order to increase the range of useful tempos possible, the Tempo Counter is preset with the byte `1xxxxxxx` (where `x` is the value of the Tempo Register) as opposed to using the Tempo Register's value directly. While this slightly complicates the formulas, it allows for much more precise tempo control.

Thus, the formula for calculating the effective tempo of a song is as follows:


$\Large T=\frac{60F}{128 \left(D_{beat}+1\right) \left(t+129\right)}$


Where $T$ is the effective tempo of the song in Beats per Minute, $F$ is the base clock frequency (usually 4 MHz), $D_{beat}$ is the total Frame Delay for 1 beat (e.g. a quarter note in 4/4 time), and $t$ is the value of the Tempo Register.
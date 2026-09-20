package cli

import (
	"io"
	"unicode/utf8"
)

// terminalSafeWriter removes terminal control sequences from human-readable
// output. It does not change task data stored on disk.
type terminalSafeWriter struct {
	destination io.Writer
}

func (w terminalSafeWriter) Write(data []byte) (int, error) {
	safe := stripTerminalControls(data)
	if len(safe) == 0 {
		return len(data), nil
	}
	n, err := w.destination.Write(safe)
	if err != nil {
		return 0, err
	}
	if n != len(safe) {
		return 0, io.ErrShortWrite
	}
	return len(data), nil
}

func stripTerminalControls(data []byte) []byte {
	result := make([]byte, 0, len(data))
	for index := 0; index < len(data); {
		if data[index] == 0x1b {
			index = skipEscapeSequence(data, index+1)
			continue
		}

		r, size := utf8.DecodeRune(data[index:])
		if r == utf8.RuneError && size == 1 {
			b := data[index]
			if b >= 0x80 && b <= 0x9f {
				index = skipC1Sequence(data, index+1, b)
				continue
			}
			result = append(result, b)
			index++
			continue
		}

		if r >= 0x80 && r <= 0x9f {
			index = skipC1Sequence(data, index+size, byte(r))
			continue
		}
		if r < 0x20 || r == 0x7f {
			if r == '\n' || r == '\t' || r == '\r' && index+size < len(data) && data[index+size] == '\n' {
				result = append(result, data[index:index+size]...)
			}
			index += size
			continue
		}

		result = append(result, data[index:index+size]...)
		index += size
	}
	return result
}

func skipC1Sequence(data []byte, next int, control byte) int {
	switch control {
	case 0x9b: // CSI
		return skipCSISequence(data, next)
	case 0x9d: // OSC
		return skipControlString(data, next, true)
	case 0x90, 0x98, 0x9e, 0x9f: // DCS, SOS, PM, APC
		return skipControlString(data, next, false)
	default:
		return next
	}
}

func skipEscapeSequence(data []byte, next int) int {
	if next >= len(data) {
		return len(data)
	}
	switch data[next] {
	case '[':
		return skipCSISequence(data, next+1)
	case ']':
		return skipControlString(data, next+1, true)
	case 'P', 'X', '^', '_':
		return skipControlString(data, next+1, false)
	}

	// Other ESC sequences have zero or more intermediate bytes followed by a
	// final byte. Strip the full sequence rather than exposing its parameters.
	for index := next; index < len(data); index++ {
		b := data[index]
		if b >= 0x30 && b <= 0x7e {
			return index + 1
		}
		if b >= 0x20 && b <= 0x2f {
			continue
		}
		if b >= 0x80 {
			_, size := utf8.DecodeRune(data[index:])
			if size > 1 {
				return index + size
			}
		}
		return index + 1
	}
	return len(data)
}

func skipCSISequence(data []byte, next int) int {
	for index := next; index < len(data); index++ {
		if data[index] >= 0x40 && data[index] <= 0x7e {
			return index + 1
		}
	}
	return len(data)
}

func skipControlString(data []byte, next int, bellTerminates bool) int {
	for index := next; index < len(data); {
		if data[index] == 0x1b {
			if index+1 < len(data) && data[index+1] == '\\' {
				return index + 2
			}
			index++
			continue
		}
		if bellTerminates && data[index] == 0x07 {
			return index + 1
		}
		if data[index] == 0x9c {
			return index + 1
		}
		r, size := utf8.DecodeRune(data[index:])
		if r == 0x9c {
			return index + size
		}
		if size == 1 && r == utf8.RuneError {
			index++
		} else {
			index += size
		}
	}
	return len(data)
}

// Go's JSON encoder escapes C0 bytes but leaves C1 Unicode code points valid
// in JSON strings. Escape C1 and DEL so the JSON remains valid and preserves
// its values without placing terminal controls on the output stream.
func escapeJSONTerminalControls(data []byte) []byte {
	result := make([]byte, 0, len(data))
	const hex = "0123456789abcdef"
	for index := 0; index < len(data); {
		r, size := utf8.DecodeRune(data[index:])
		if size == 1 && r == utf8.RuneError {
			result = append(result, data[index])
			index++
			continue
		}
		if r >= 0x7f && r <= 0x9f {
			result = append(result, '\\', 'u', '0', '0', hex[byte(r)>>4], hex[byte(r)&0x0f])
		} else {
			result = append(result, data[index:index+size]...)
		}
		index += size
	}
	return result
}

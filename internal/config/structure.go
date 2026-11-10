package config

import "errors"

// Bounds on the shape of a configuration file, checked before decoding.
const (
	// MaxNesting is the deepest a table or array may nest.
	MaxNesting = 8
	// MaxKeySegments is the most dotted segments one key may have.
	MaxKeySegments = 8
	// MaxKeyBytes is the longest one key may be, quotes included.
	MaxKeyBytes = 256
)

var (
	errTooDeep      = errors.New("nests tables or arrays more than 8 levels deep")
	errKeySegments  = errors.New("has a key with more than 8 dotted segments")
	errKeyTooLong   = errors.New("has a key longer than 256 bytes")
	errUnterminated = errors.New("has an unterminated string")
)

// checkStructure bounds the shape of a TOML document in one pass over its
// bytes, before the decoder sees it. The decoder allocates for every
// nesting level and every dotted segment, so a small hostile file can cost
// seconds and gigabytes if it reaches the decoder. The scan skips comments
// and the four string forms, so brackets and dots inside values do not
// count; everything else that looks like structure does. A file this
// refuses is one the decoder would refuse or struggle with.
func checkStructure(data []byte) error {
	var (
		stack     []byte // open brackets: '[', '{', or 'H' for a table header
		inKey     = true // the current run of bytes is in key position
		keyLen    int
		keyDots   int
		lineStart = true // no byte but whitespace seen on this line yet
	)
	resetKey := func() { keyLen, keyDots = 0, 0 }
	push := func(c byte) error {
		if len(stack) >= MaxNesting {
			return errTooDeep
		}
		stack = append(stack, c)
		return nil
	}
	pop := func() {
		if len(stack) > 0 {
			stack = stack[:len(stack)-1]
		}
	}

	for i := 0; i < len(data); i++ {
		c := data[i]
		switch c {
		case '#':
			for i < len(data) && data[i] != '\n' {
				i++
			}
			i-- // the loop step lands on the newline
			continue
		case '"', '\'':
			end, err := skipString(data, i)
			if err != nil {
				return err
			}
			if inKey {
				keyLen += end - i
				if keyLen > MaxKeyBytes {
					return errKeyTooLong
				}
			}
			i = end - 1
			lineStart = false
			continue
		case '\n':
			if len(stack) == 0 {
				inKey = true
				resetKey()
			}
			lineStart = true
			continue
		case ' ', '\t', '\r':
			continue
		}

		atLineStart := lineStart
		lineStart = false
		switch c {
		case '[':
			if atLineStart && len(stack) == 0 || len(stack) > 0 && stack[len(stack)-1] == 'H' {
				// A table header, or the second bracket of an
				// array-of-tables header: its content is keys.
				if err := push('H'); err != nil {
					return err
				}
				inKey = true
			} else {
				if err := push('['); err != nil {
					return err
				}
				inKey = false
			}
			resetKey()
		case '{':
			if err := push('{'); err != nil {
				return err
			}
			inKey = true
			resetKey()
		case ']', '}':
			pop()
			inKey = false
			resetKey()
		case '=':
			inKey = false
			resetKey()
		case ',':
			inKey = len(stack) > 0 && stack[len(stack)-1] == '{'
			resetKey()
		case '.':
			if inKey {
				keyDots++
				if keyDots >= MaxKeySegments {
					return errKeySegments
				}
				keyLen++
				if keyLen > MaxKeyBytes {
					return errKeyTooLong
				}
			}
		default:
			if inKey {
				keyLen++
				if keyLen > MaxKeyBytes {
					return errKeyTooLong
				}
			}
		}
	}
	return nil
}

// skipString returns the index just past the string that opens at data[i],
// following the four TOML string forms: basic ("..."), literal ('...'),
// multi-line basic ("""..."""), and multi-line literal (three single
// quotes on each side).
func skipString(data []byte, i int) (int, error) {
	q := data[i]
	triple := i+2 < len(data) && data[i+1] == q && data[i+2] == q
	if triple {
		j := i + 3
		for j < len(data) {
			if q == '"' && data[j] == '\\' {
				j += 2
				continue
			}
			if j+2 < len(data) && data[j] == q && data[j+1] == q && data[j+2] == q {
				j += 3
				// Up to two more delimiters belong to the content.
				for n := 0; n < 2 && j < len(data) && data[j] == q; n++ {
					j++
				}
				return j, nil
			}
			j++
		}
		return 0, errUnterminated
	}
	j := i + 1
	for j < len(data) {
		switch {
		case data[j] == '\n':
			return 0, errUnterminated
		case q == '"' && data[j] == '\\':
			j += 2
		case data[j] == q:
			return j + 1, nil
		default:
			j++
		}
	}
	return 0, errUnterminated
}

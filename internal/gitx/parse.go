package gitx

import "bytes"

// ParseZ splits NUL-terminated git output into entries. The final
// terminator produces no extra entry; a doubled NUL (as git status
// --porcelain=v2 -z uses for renames) yields an empty entry that callers
// must expect.
func ParseZ(out []byte) []string {
	if len(out) == 0 {
		return nil
	}
	out = bytes.TrimSuffix(out, []byte{0})
	parts := bytes.Split(out, []byte{0})
	entries := make([]string, len(parts))
	for i, p := range parts {
		entries[i] = string(p)
	}
	return entries
}

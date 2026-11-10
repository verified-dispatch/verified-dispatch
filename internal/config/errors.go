package config

import "fmt"

// Refusal is a configuration problem the run reports as REFUSED: a value
// the rules do not allow, from any layer, including a repository key
// outside the allow list and a home cap out of range. Reason is one plain
// sentence that names the layer and the key and, for a bound, the value and
// the bound. A file the tool cannot use (unreadable, oversized, badly
// shaped, undecodable, with an unknown key or an invalid preset) is an
// ordinary error from any layer.
type Refusal struct {
	Reason string
}

func (e *Refusal) Error() string { return "refused: " + e.Reason }

func refusef(format string, a ...any) error {
	return &Refusal{Reason: fmt.Sprintf(format, a...)}
}

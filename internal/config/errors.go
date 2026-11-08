package config

import "fmt"

// Refusal is a configuration problem the run reports as REFUSED: a value
// the rules do not allow, from any layer. Reason is one plain sentence that
// names the layer and the key and, for a cap, the value and the cap.
// Problems reading a file the user owns are ordinary errors instead.
type Refusal struct {
	Reason string
}

func (e *Refusal) Error() string { return "refused: " + e.Reason }

func refusef(format string, a ...any) error {
	return &Refusal{Reason: fmt.Sprintf(format, a...)}
}

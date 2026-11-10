package config

import (
	"regexp"
	"regexp/syntax"
)

// patternCost compiles p the way the regexp package does and returns the
// instruction count of its program. Matching is linear in input length
// times program size, so the count bounds the time a pattern can take.
func patternCost(p string) (int, error) {
	re, err := syntax.Parse(p, syntax.Perl)
	if err != nil {
		return 0, err
	}
	prog, err := syntax.Compile(re.Simplify())
	if err != nil {
		return 0, err
	}
	return len(prog.Inst), nil
}

// checkPattern refuses a pattern that is too long, does not compile, is too
// costly, or matches the empty string. who names the layer and key names
// the list; both appear in the refusal.
func checkPattern(who, key, p string) error {
	if len(p) > MaxPatternBytes {
		return refusef("%s sets %s with an entry of %d bytes, above the limit %d", who, key, len(p), MaxPatternBytes)
	}
	cost, err := patternCost(p)
	if err != nil {
		return refusef("%s sets %s with an entry that does not compile: %v", who, key, err)
	}
	if cost > MaxPatternInstructions {
		return refusef("%s sets %s with an entry of %d instructions, above the limit %d", who, key, cost, MaxPatternInstructions)
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return refusef("%s sets %s with an entry that does not compile: %v", who, key, err)
	}
	if re.MatchString("") {
		return refusef("%s sets %s with an entry that matches the empty string, which would match every line", who, key)
	}
	return nil
}

// checkPatternBudget refuses when the patterns of one run together exceed
// the run bound. Each pattern passed checkPattern already, so compiling
// again here is cheap.
func checkPatternBudget(lists ...[]string) error {
	total := 0
	for _, l := range lists {
		for _, p := range l {
			cost, err := patternCost(p)
			if err != nil {
				return refusef("pattern %q does not compile: %v", short(p), err)
			}
			total += cost
		}
	}
	if total > MaxRunPatternInstructions {
		return refusef("the patterns of this run total %d instructions across all patterns, above the limit %d", total, MaxRunPatternInstructions)
	}
	return nil
}

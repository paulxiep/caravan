package compiler

import (
	"fmt"
	"io"
	"strings"
)

// Severity classifies a Diagnostic.
type Severity int

const (
	SeverityError Severity = iota
	SeverityWarning
)

func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	default:
		panic(fmt.Sprintf("unknown Severity: %d", s))
	}
}

// Diagnostic is one error or warning attached to a source position.
type Diagnostic struct {
	Severity Severity
	Span     Span
	Message  string
}

// Diagnostics is a collected list of Diagnostic. Cheap to pass around.
// Use HasErrors() to gate phase progression.
type Diagnostics struct {
	items []Diagnostic
}

// Error appends an error-severity diagnostic.
func (d *Diagnostics) Error(span Span, format string, args ...any) {
	d.items = append(d.items, Diagnostic{
		Severity: SeverityError,
		Span:     span,
		Message:  fmt.Sprintf(format, args...),
	})
}

// Warn appends a warning-severity diagnostic.
func (d *Diagnostics) Warn(span Span, format string, args ...any) {
	d.items = append(d.items, Diagnostic{
		Severity: SeverityWarning,
		Span:     span,
		Message:  fmt.Sprintf(format, args...),
	})
}

// Items returns the collected diagnostics in append order.
func (d *Diagnostics) Items() []Diagnostic { return d.items }

// HasErrors reports whether any error-severity diagnostic was recorded.
func (d *Diagnostics) HasErrors() bool {
	for _, it := range d.items {
		if it.Severity == SeverityError {
			return true
		}
	}
	return false
}

// WriteTo prints the diagnostics in `file:line:col: severity: message`
// form, one per line, in append order.
func (d *Diagnostics) WriteTo(w io.Writer) (int64, error) {
	var total int64
	for _, it := range d.items {
		loc := formatLoc(it.Span)
		line := fmt.Sprintf("%s: %s: %s\n", loc, it.Severity, it.Message)
		n, err := io.WriteString(w, line)
		total += int64(n)
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func formatLoc(s Span) string {
	if s.File == "" {
		return "<unknown>"
	}
	parts := []string{s.File}
	if s.Line > 0 {
		parts = append(parts, fmt.Sprintf("%d", s.Line))
		if s.Col > 0 {
			parts = append(parts, fmt.Sprintf("%d", s.Col))
		}
	}
	return strings.Join(parts, ":")
}

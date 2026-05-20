package compiler

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// RawYAML is the output of phase 1 (Lex). It wraps the raw yaml.Node
// tree plus the source filename so later phases can attach diagnostic
// spans (yaml.Node carries Line / Column but not the file path).
type RawYAML struct {
	File string
	Root *yaml.Node
}

// LexFile reads file from disk and parses it into a yaml.Node tree.
// Returns an error only on filesystem or YAML-syntax failures — these
// are pre-schema concerns that block all later phases.
func LexFile(file string) (*RawYAML, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	return LexBytes(file, data)
}

// LexBytes parses bytes as YAML with file as the source-position label.
// Useful in tests.
func LexBytes(file string, data []byte) (*RawYAML, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", file, err)
	}
	return &RawYAML{File: file, Root: &root}, nil
}

// nodeSpan returns a Span for the given yaml.Node + source file.
func nodeSpan(file string, n *yaml.Node) Span {
	if n == nil {
		return Span{File: file}
	}
	return Span{File: file, Line: n.Line, Col: n.Column}
}

// docRoot returns the content node of a document-level yaml.Node, or
// the node itself if it's already the root mapping. yaml.Unmarshal
// produces a DocumentNode with one Content entry — we want that entry.
func docRoot(root *yaml.Node) *yaml.Node {
	if root == nil {
		return nil
	}
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		return root.Content[0]
	}
	return root
}

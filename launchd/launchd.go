// Package launchd renders the launchd job definition for the presence agent.
// Installing the rendered file is the job of "ticktack init", not this package.
package launchd

import (
	"bytes"
	_ "embed"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/template"
)

// Label is the launchd job label.
const Label = "dev.ticktack.agent"

//go:embed dev.ticktack.agent.plist.tmpl
var agentTemplate string

// Params are the values substituted into the plist template.
type Params struct {
	// Binary is the absolute path of the ticktack executable. It should be a
	// path that survives upgrades, not a versioned install directory.
	Binary string
	// LogPath is where stdout and stderr of the agent are written.
	LogPath string
	// ConfigHome, DataHome and StateHome are the XDG base directories the
	// agent runs with. launchd does not inherit the environment of the shell,
	// so they are pinned to keep the agent on the same config file, database
	// and pause flag as the CLI. An empty value is left out of the plist; the
	// agent then falls back to the default below the home directory.
	ConfigHome string
	DataHome   string
	StateHome  string
}

// RenderAgentPlist renders the agent plist. Values are XML-escaped.
func RenderAgentPlist(p Params) (string, error) {
	escaped := Params{}
	for _, field := range []struct {
		dst *string
		src string
	}{
		{&escaped.Binary, p.Binary},
		{&escaped.LogPath, p.LogPath},
		{&escaped.ConfigHome, p.ConfigHome},
		{&escaped.DataHome, p.DataHome},
		{&escaped.StateHome, p.StateHome},
	} {
		value, err := escapeXML(field.src)
		if err != nil {
			return "", err
		}
		*field.dst = value
	}
	tmpl, err := template.New("agent").Parse(agentTemplate)
	if err != nil {
		return "", fmt.Errorf("parse plist template: %w", err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, escaped); err != nil {
		return "", fmt.Errorf("render plist template: %w", err)
	}
	return b.String(), nil
}

func escapeXML(s string) (string, error) {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return "", fmt.Errorf("escape %q: %w", s, err)
	}
	return b.String(), nil
}

// ErrNoProgram reports a plist without a program to run.
var ErrNoProgram = errors.New("plist names no program")

// ProgramPath returns the program a launchd plist runs: the first entry of
// its ProgramArguments array, with XML escapes resolved.
func ProgramPath(plist []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(plist))
	afterKey, inArguments := false, false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return "", ErrNoProgram
		}
		if err != nil {
			return "", fmt.Errorf("parse plist: %w", err)
		}
		switch el := tok.(type) {
		case xml.StartElement:
			switch {
			case el.Name.Local == "key":
				var key string
				if err := dec.DecodeElement(&key, &el); err != nil {
					return "", fmt.Errorf("parse plist: %w", err)
				}
				afterKey = strings.TrimSpace(key) == "ProgramArguments"
			case el.Name.Local == "array" && afterKey:
				inArguments = true
			case el.Name.Local == "string" && inArguments:
				var program string
				if err := dec.DecodeElement(&program, &el); err != nil {
					return "", fmt.Errorf("parse plist: %w", err)
				}
				if program = strings.TrimSpace(program); program == "" {
					return "", ErrNoProgram
				}
				return program, nil
			default:
				afterKey = false
			}
		case xml.EndElement:
			if inArguments && el.Name.Local == "array" {
				return "", ErrNoProgram
			}
		}
	}
}

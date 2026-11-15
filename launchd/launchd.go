// Package launchd renders the launchd job definition for the presence agent.
// Installing the rendered file is the job of "ticktack init", not this package.
package launchd

import (
	_ "embed"
	"encoding/xml"
	"fmt"
	"strings"
	"text/template"
)

// Label is the launchd job label.
const Label = "dev.ticktack.agent"

//go:embed dev.ticktack.agent.plist.tmpl
var agentTemplate string

// Params are the values substituted into the plist template.
type Params struct {
	// Binary is the absolute path of the ticktack executable.
	Binary string
	// LogPath is where stdout and stderr of the agent are written.
	LogPath string
}

// RenderAgentPlist renders the agent plist. Values are XML-escaped.
func RenderAgentPlist(p Params) (string, error) {
	binary, err := escapeXML(p.Binary)
	if err != nil {
		return "", err
	}
	logPath, err := escapeXML(p.LogPath)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New("agent").Parse(agentTemplate)
	if err != nil {
		return "", fmt.Errorf("parse plist template: %w", err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, Params{Binary: binary, LogPath: logPath}); err != nil {
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

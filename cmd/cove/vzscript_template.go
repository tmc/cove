package main

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/template"
)

type templateVarFlag map[string]any

func (f *templateVarFlag) String() string {
	if f == nil || len(*f) == 0 {
		return ""
	}
	var pairs []string
	for k, v := range *f {
		pairs = append(pairs, fmt.Sprintf("%s=%v", k, v))
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}

func (f *templateVarFlag) Set(s string) error {
	name, value, ok := strings.Cut(s, "=")
	if !ok {
		return fmt.Errorf("template var must be name=value")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("template var name is empty")
	}
	if *f == nil {
		*f = map[string]any{}
	}
	(*f)[name] = parseTemplateValue(value)
	return nil
}

func parseTemplateValue(s string) any {
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	}
	return s
}

func renderVZScriptTemplate(data []byte, name string, vars map[string]any) ([]byte, error) {
	tmpl, err := template.New(name).Option("missingkey=zero").Funcs(template.FuncMap{
		"env":         os.Getenv,
		"queryescape": url.QueryEscape,
		"quote":       scriptQuote,
	}).Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, vars); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}
	return out.Bytes(), nil
}

func maybeRenderVZScript(data []byte, name string, cfg vzscriptConfig) ([]byte, error) {
	if !cfg.template {
		return data, nil
	}
	return renderVZScriptTemplate(data, name, cfg.templateVars)
}

func scriptQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Package template renders the outbox event types into emails.
//
// Indonesian and English (FR-32). The locale rides on the payload so the
// language is decided where the customer's preference is known, not guessed
// here from an address.
//
// Bodies are built with text/template, not html/template, for the text part
// and html/template for the HTML part — the HTML one escapes interpolated
// values, which matters because a label like a friend's name comes from user
// input and would otherwise be an injection vector in the recipient's inbox.
package template

import (
	"bytes"
	"fmt"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
)

// Rendered is a subject and the two body representations.
type Rendered struct {
	Subject  string
	HTMLBody string
	TextBody string
}

// definition is one email in one language.
type definition struct {
	subject string
	html    string
	text    string
}

// render executes a definition against its data.
func (d definition) render(data any) (Rendered, error) {
	subject, err := renderText("subject", d.subject, data)
	if err != nil {
		return Rendered{}, err
	}
	html, err := renderHTML("html", d.html, data)
	if err != nil {
		return Rendered{}, err
	}
	text, err := renderText("text", d.text, data)
	if err != nil {
		return Rendered{}, err
	}
	return Rendered{Subject: strings.TrimSpace(subject), HTMLBody: html, TextBody: text}, nil
}

func renderText(name, tpl string, data any) (string, error) {
	t, err := texttemplate.New(name).Parse(tpl)
	if err != nil {
		return "", fmt.Errorf("template %s: parse: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template %s: execute: %w", name, err)
	}
	return buf.String(), nil
}

func renderHTML(name, tpl string, data any) (string, error) {
	t, err := htmltemplate.New(name).Parse(tpl)
	if err != nil {
		return "", fmt.Errorf("template %s: parse: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template %s: execute: %w", name, err)
	}
	return buf.String(), nil
}

// ErrNoTemplate means the type has no email defined for it.
//
// Distinct from a render failure: an unknown type is a producer deployed ahead
// of this consumer, which retrying will not fix.
type ErrNoTemplate struct{ Type events.Type }

func (e ErrNoTemplate) Error() string { return "no email template for type " + string(e.Type) }

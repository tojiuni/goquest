package tojihub

import (
	"fmt"
	"strings"

	"github.com/tojiuni/goquest/internal/calendarsync"
)

// icalTimeFormat is iCal's UTC "floating" basic format: YYYYMMDDTHHMMSSZ.
const icalTimeFormat = "20060102T150405Z"

// toICal renders ev as a minimal VCALENDAR/VEVENT text block. UID matches
// ev.ObjectID without the ".ics" suffix, per the goquest-tojihub contract
// (geneso:neunexus/spec/2026-08-09-goquest-tojihub-integration.md).
func toICal(ev calendarsync.Event) string {
	uid := strings.TrimSuffix(ev.ObjectID, ".ics")
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("BEGIN:VEVENT\r\n")
	fmt.Fprintf(&b, "UID:%s\r\n", uid)
	fmt.Fprintf(&b, "SUMMARY:%s\r\n", escapeICalText(ev.Summary))
	fmt.Fprintf(&b, "DTSTART:%s\r\n", ev.DTStart.UTC().Format(icalTimeFormat))
	b.WriteString("END:VEVENT\r\n")
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

// escapeICalText escapes the RFC 5545 TEXT special characters that can
// appear in a ticket title: backslash, semicolon, comma, and newline.
// Order matters — backslash must be escaped first, or the escapes added
// for the other characters would themselves get re-escaped.
func escapeICalText(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, ";", `\;`)
	s = strings.ReplaceAll(s, ",", `\,`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

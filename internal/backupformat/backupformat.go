// Package backupformat is the single place that knows what backup formats
// restoredrill supports and how to recognize each from its bytes.
package backupformat

import (
	"bytes"
	"sort"
)

// Format describes one supported backup format.
type Format struct {
	// Sniff reports whether head looks like this format. Nil if the
	// format has no reliable signature (e.g. plain SQL has no header).
	Sniff func(head []byte) bool
}

var known = map[string]Format{
	"pg_dump_custom": {Sniff: sniffPgDumpCustom},
	"pg_dump_sql":    {},
}

// pg_dump -Fc archives start with "PGDMP".
func sniffPgDumpCustom(head []byte) bool {
	return bytes.HasPrefix(head, []byte("PGDMP"))
}

func Valid(name string) bool {
	_, ok := known[name]
	return ok
}

// Names returns every known format name, sorted.
func Names() []string {
	names := make([]string, 0, len(known))
	for n := range known {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func Sniffable(name string) bool {
	f, ok := known[name]
	return ok && f.Sniff != nil
}

// Matches reports whether head looks like format name. False if name is
// unknown or not sniffable.
func Matches(name string, head []byte) bool {
	f, ok := known[name]
	return ok && f.Sniff != nil && f.Sniff(head)
}

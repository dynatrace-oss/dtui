package catalog

import "strings"

// EchoCommand renders a command echo: a line that a POSIX shell parses back
// into command's words, the operand and the flags, and nothing else. Every
// echo that carries a value is meant to go through here; two do not yet
// (echoQuery in the tui's datasource.go, and the snapshots echo in
// livedebugger.go; docs/dev/tech-debt.md). The operand and the flags are
// values (a query, an id from a record, a segment) and whoever writes the
// tenant's data picks them, so each is quoted unless it is plainly a single
// word; command is the fixed `dtctl <verb> <resource>` text and is not.
//
// An operand that starts with a dash would be read as a flag, so it moves
// behind the flags and an end-of-options marker.
func EchoCommand(command, operand string, flags ...string) string {
	words := []string{command}
	dashed := strings.HasPrefix(operand, "-")
	if !dashed {
		words = append(words, shellWord(operand))
	}
	for _, f := range flags {
		words = append(words, shellWord(f))
	}
	if dashed {
		words = append(words, "--", shellWord(operand))
	}
	return strings.Join(words, " ")
}

// shellWord returns s as one shell word, left bare when no character in it
// means anything to a shell.
func shellWord(s string) string {
	if s != "" && strings.Trim(s, shellBare) == "" {
		return s
	}
	return shellSingleQuote(s)
}

// shellBare are the characters a shell reads literally anywhere in a word.
// The equals sign is not one of them: zsh replaces a word that starts with it
// by the path of the command it names (`=ls` becomes `/bin/ls`).
const shellBare = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-+:,./@%"

// shellSingleQuote wraps s in single quotes for a POSIX shell, closing and
// reopening the quotes around any embedded single quote.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

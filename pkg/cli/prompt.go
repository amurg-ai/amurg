// Package cli provides interactive terminal prompt helpers for CLI wizards.
package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// Prompter handles interactive terminal prompts.
type Prompter struct {
	In      io.Reader
	Out     io.Writer
	scanner *bufio.Scanner
}

// DefaultPrompter returns a Prompter connected to stdin/stdout.
func DefaultPrompter() *Prompter {
	return &Prompter{In: os.Stdin, Out: os.Stdout}
}

func (p *Prompter) scan() *bufio.Scanner {
	if p.scanner == nil {
		p.scanner = bufio.NewScanner(p.In)
	}
	return p.scanner
}

// readLine reads a single trimmed line from the scanner.
func (p *Prompter) readLine() string {
	if p.scan().Scan() {
		return strings.TrimSpace(p.scan().Text())
	}
	return ""
}

// Ask prints a question with a default value and reads one line.
// Returns the default if the user presses Enter without typing.
func (p *Prompter) Ask(question, defaultVal string) string {
	if defaultVal != "" {
		_, _ = fmt.Fprintf(p.Out, "%s [%s]: ", question, defaultVal)
	} else {
		_, _ = fmt.Fprintf(p.Out, "%s: ", question)
	}
	line := p.readLine()
	if line != "" {
		return line
	}
	return defaultVal
}

// AskPassword reads a line without echoing. Falls back to plain read if
// stdin is not a terminal (e.g. during tests or piped input).
func (p *Prompter) AskPassword(question string) string {
	_, _ = fmt.Fprintf(p.Out, "%s: ", question)

	// Try to read without echo if stdin is a real terminal.
	if f, ok := p.In.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		b, err := term.ReadPassword(int(f.Fd()))
		_, _ = fmt.Fprintln(p.Out) // newline after hidden input
		if err == nil {
			return strings.TrimSpace(string(b))
		}
	}

	// Fallback: plain read.
	return p.readLine()
}

// AskInt asks for an integer with a default value.
func (p *Prompter) AskInt(question string, defaultVal int) int {
	for {
		ans := p.Ask(question, strconv.Itoa(defaultVal))
		n, err := strconv.Atoi(ans)
		if err == nil && n > 0 {
			return n
		}
		_, _ = fmt.Fprintf(p.Out, "  Please enter a positive number.\n")
	}
}

// Choose presents a numbered list of options and returns the selected value.
func (p *Prompter) Choose(question string, options []string, defaultIdx int) string {
	_, _ = fmt.Fprintf(p.Out, "%s\n", question)
	for i, opt := range options {
		marker := "  "
		if i == defaultIdx {
			marker = "> "
		}
		_, _ = fmt.Fprintf(p.Out, "%s%d) %s\n", marker, i+1, opt)
	}

	for {
		ans := p.Ask("Choice", strconv.Itoa(defaultIdx+1))
		n, err := strconv.Atoi(ans)
		if err == nil && n >= 1 && n <= len(options) {
			return options[n-1]
		}
		_, _ = fmt.Fprintf(p.Out, "  Please enter a number between 1 and %d.\n", len(options))
	}
}

// Confirm asks a yes/no question.
func (p *Prompter) Confirm(question string, defaultYes bool) bool {
	hint := "y/N"
	if defaultYes {
		hint = "Y/n"
	}
	ans := p.Ask(fmt.Sprintf("%s [%s]", question, hint), "")
	if ans == "" {
		return defaultYes
	}
	return strings.HasPrefix(strings.ToLower(ans), "y")
}

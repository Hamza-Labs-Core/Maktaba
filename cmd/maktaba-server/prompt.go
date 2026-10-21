package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// prompter wraps stdin for the interactive wizards. A single buffered
// reader is shared so partial lines don't get lost between questions.
type prompter struct {
	r   *bufio.Reader
	tty bool // stdin: a secret is read without echo when it is a terminal
}

func newPrompter() *prompter { return &prompter{r: bufio.NewReader(os.Stdin), tty: true} }

// newPrompterFrom answers from r (tests, a file of answers).
func newPrompterFrom(r io.Reader) *prompter { return &prompter{r: bufio.NewReader(r)} }

// line asks a question with an optional default (shown in brackets) and
// returns the trimmed answer, or def when the user just hits enter.
func (p *prompter) line(question, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", question, def)
	} else {
		fmt.Printf("%s: ", question)
	}
	text, _ := p.r.ReadString('\n')
	text = strings.TrimSpace(text)
	if text == "" {
		return def
	}
	return text
}

// yesNo asks a yes/no question, defaulting to def when the user hits
// enter.
func (p *prompter) yesNo(question string, def bool) bool {
	suffix := "[y/N]"
	if def {
		suffix = "[Y/n]"
	}
	fmt.Printf("%s %s: ", question, suffix)
	text, _ := p.r.ReadString('\n')
	text = strings.ToLower(strings.TrimSpace(text))
	switch text {
	case "":
		return def
	case "y", "yes":
		return true
	case "n", "no":
		return false
	default:
		return def
	}
}

// secret reads a line without echoing it (passwords). Falls back to a
// visible read when stdin is not a terminal (piped input).
func (p *prompter) secret(question string) string {
	fmt.Printf("%s: ", question)
	fd := int(os.Stdin.Fd())
	if p.tty && term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Println()
		if err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	text, _ := p.r.ReadString('\n')
	return strings.TrimSpace(text)
}

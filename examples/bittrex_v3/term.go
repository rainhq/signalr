package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/rainhq/signalr/v2/bittrex"
)

type Terminal struct {
	Escape *EscapeCodes

	delegate   *term.Terminal
	state      *term.State
	hideCursor bool
}

func NewTerminal(opts ...TermOpt) (*Terminal, error) {
	state, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil, err
	}

	delegate := term.NewTerminal(os.Stdin, "")

	t := Terminal{
		Escape: &EscapeCodes{
			EscapeCodes: *delegate.Escape,
			Clear:       []byte("\033[2J\033[H"),
			Bold:        []byte("\033[1m"),
			HideCursor:  []byte("\033[?25l"),
			ShowCursor:  []byte("\033[?25h"),
		},
		delegate: delegate,
		state:    state,
	}

	for _, opt := range opts {
		opt(&t)
	}

	if t.hideCursor {
		fmt.Fprint(t.delegate, string(t.Escape.HideCursor))
	}

	return &t, nil
}

func (t *Terminal) Write(buf []byte) (int, error) {
	return t.delegate.Write(buf)
}

func (t *Terminal) PrintEscape(esc []byte, s string) {
	fmt.Fprint(t.delegate, string(esc), s, string(t.Escape.Reset))
}

func (t *Terminal) Clear() {
	fmt.Fprint(t.delegate, string(t.Escape.Clear))
}

func (t *Terminal) ReadLine() (string, error) {
	return t.delegate.ReadLine()
}

func (t *Terminal) Close() error {
	if t.hideCursor {
		fmt.Fprint(t.delegate, string(t.Escape.ShowCursor))
	}

	return term.Restore(int(os.Stdin.Fd()), t.state)
}

type TermOpt func(*Terminal)

func HideCursor() TermOpt {
	return func(t *Terminal) {
		t.hideCursor = true
	}
}

type EscapeCodes struct {
	term.EscapeCodes
	Clear      []byte
	Bold       []byte
	HideCursor []byte
	ShowCursor []byte
}

func actionToColor(t *Terminal, action bittrex.Action) []byte {
	switch action {
	case bittrex.DeleteAction:
		return t.Escape.Red
	case bittrex.AddAction:
		return t.Escape.Green
	case bittrex.UpdateAction:
		return t.Escape.Blue
	default:
		return nil
	}
}

func center(s string, l int) string {
	padding := l - len([]rune(s))
	lpad := padding / 2

	b := strings.Builder{}

	for i := 0; i < lpad; i++ {
		_, _ = b.WriteRune(' ')
	}

	_, _ = b.WriteString(s)

	for i := 0; i < padding-lpad; i++ {
		_, _ = b.WriteRune(' ')
	}

	return b.String()
}

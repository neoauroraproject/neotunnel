package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// A small full-screen terminal UI: arrow-key menus and line inputs in raw
// mode, and a normal (cooked) terminal while actions print their output.
// It reads from /dev/tty when stdin is not a terminal, so the menu also
// works right after "curl ... | sudo bash".

var errNoTTY = errors.New("no terminal available")

type tui struct {
	in  *os.File
	r   *bufio.Reader
	fd  int
	raw *term.State // saved cooked state while in raw mode
}

func openTUI() (*tui, error) {
	in := os.Stdin
	if !term.IsTerminal(int(in.Fd())) {
		f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return nil, errNoTTY
		}
		in = f
	}
	if !term.IsTerminal(int(in.Fd())) {
		return nil, errNoTTY
	}
	return &tui{in: in, r: bufio.NewReader(in), fd: int(in.Fd())}, nil
}

func (t *tui) close() {
	t.cooked()
	fmt.Print("\x1b[?25h")
	if t.in != os.Stdin {
		t.in.Close()
	}
}

func (t *tui) rawMode() {
	if t.raw == nil {
		if st, err := term.MakeRaw(t.fd); err == nil {
			t.raw = st
		}
	}
}

func (t *tui) cooked() {
	if t.raw != nil {
		term.Restore(t.fd, t.raw)
		t.raw = nil
	}
}

// write prints s, translating newlines while the terminal is raw.
func (t *tui) write(s string) {
	if t.raw != nil {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	fmt.Print(s)
}

func (t *tui) width() int {
	if w, _, err := term.GetSize(t.fd); err == nil && w > 20 {
		return w
	}
	return 80
}

// clearScreen keeps the scrollback, so earlier output (like the panel
// password from the installer) can still be scrolled to.
func clearScreen() string { return "\x1b[H\x1b[2J" }

type key int

const (
	keyNone key = iota
	keyUp
	keyDown
	keyEnter
	keyRight
	keyBack  // ←, Esc, Backspace
	keyQuit  // Ctrl+C
	keyRune  // printable
	keyErase // Backspace inside an input
)

// readKey decodes one keypress. A lone Esc is told apart from an arrow
// sequence by whether more bytes arrived with it.
func (t *tui) readKey() (key, rune) {
	b, err := t.r.ReadByte()
	if err != nil {
		return keyQuit, 0
	}
	switch b {
	case 3, 4: // Ctrl+C, Ctrl+D
		return keyQuit, 0
	case '\r', '\n':
		return keyEnter, 0
	case 127, 8:
		return keyErase, 0
	case 27:
		if t.r.Buffered() == 0 {
			return keyBack, 0
		}
		b2, _ := t.r.ReadByte()
		if b2 != '[' && b2 != 'O' {
			return keyNone, 0
		}
		b3, _ := t.r.ReadByte()
		// Swallow the rest of longer sequences (e.g. "\x1b[3~").
		for b3 >= '0' && b3 <= '9' || b3 == ';' {
			b3, _ = t.r.ReadByte()
		}
		switch b3 {
		case 'A':
			return keyUp, 0
		case 'B':
			return keyDown, 0
		case 'C':
			return keyRight, 0
		case 'D':
			return keyBack, 0
		}
		return keyNone, 0
	}
	if b < 32 {
		return keyNone, 0
	}
	t.r.UnreadByte()
	r, _, err := t.r.ReadRune()
	if err != nil || r == utf8.RuneError {
		return keyNone, 0
	}
	return keyRune, r
}

type item struct {
	label string
	hint  string
	off   bool // shown dimmed, not selectable
}

// choose shows a menu and returns the chosen index, or -1 to go back.
// Digits 1-9 pick an item directly; 0 or q goes back.
func (t *tui) choose(header, title string, items []item, sel int) int {
	t.rawMode()
	t.write("\x1b[?25l")
	defer t.write("\x1b[?25h")
	if sel < 0 || sel >= len(items) {
		sel = 0
	}
	for items[sel].off && sel < len(items)-1 {
		sel++
	}
	labelW := 0
	for _, it := range items {
		labelW = max(labelW, utf8.RuneCountInString(it.label))
	}
	for {
		var b strings.Builder
		b.WriteString(clearScreen())
		b.WriteString(header)
		b.WriteString("\n " + bold(title) + "\n\n")
		for i, it := range items {
			num := "   "
			if i < 9 {
				num = fmt.Sprintf("%2d ", i+1)
			}
			label := it.label + strings.Repeat(" ", labelW-utf8.RuneCountInString(it.label))
			hint := ""
			if it.hint != "" && t.width() > labelW+20 {
				hint = "  " + truncate(it.hint, t.width()-labelW-14)
			}
			switch {
			case it.off:
				b.WriteString("   " + dim(num+label+hint) + "\n")
			case i == sel:
				b.WriteString(" " + accent("❯ ") + accent(num) + bold(label) + dim(hint) + "\n")
			default:
				b.WriteString("   " + dim(num) + label + dim(hint) + "\n")
			}
		}
		b.WriteString("\n " + dim("↑↓ move   enter select   ← / esc back   ctrl+c quit") + "\n")
		t.write(b.String())

		k, r := t.readKey()
		switch k {
		case keyUp:
			for i := sel - 1; i >= 0; i-- {
				if !items[i].off {
					sel = i
					break
				}
			}
		case keyDown:
			for i := sel + 1; i < len(items); i++ {
				if !items[i].off {
					sel = i
					break
				}
			}
		case keyEnter, keyRight:
			if !items[sel].off {
				return sel
			}
		case keyBack, keyErase:
			return -1
		case keyQuit:
			return -2
		case keyRune:
			switch {
			case r == 'q' || r == '0':
				return -1
			case r >= '1' && r <= '9':
				if i := int(r - '1'); i < len(items) && !items[i].off {
					return i
				}
			case r == 'k':
				if sel > 0 && !items[sel-1].off {
					sel--
				}
			case r == 'j':
				if sel < len(items)-1 && !items[sel+1].off {
					sel++
				}
			}
		}
	}
}

func truncate(s string, n int) string {
	if n < 4 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// input reads a line with a default. ok is false when the user cancels
// (Esc or Ctrl+C). validate may reject the value with a message.
func (t *tui) input(label, def string, validate func(string) error) (string, bool) {
	t.rawMode()
	for {
		prompt := " " + accent("›") + " " + label
		if def != "" {
			prompt += " " + dim("["+def+"]")
		}
		t.write(prompt + ": ")
		var buf []rune
	read:
		for {
			k, r := t.readKey()
			switch k {
			case keyEnter:
				t.write("\n")
				break read
			case keyBack, keyQuit:
				t.write("\n")
				return "", false
			case keyErase:
				if len(buf) > 0 {
					buf = buf[:len(buf)-1]
					t.write("\b \b")
				}
			case keyRune:
				buf = append(buf, r)
				t.write(string(r))
			}
		}
		v := strings.TrimSpace(string(buf))
		if v == "" {
			v = def
		}
		if validate != nil {
			if err := validate(v); err != nil {
				t.write("   " + red("✗ "+err.Error()) + "\n")
				continue
			}
		}
		return v, true
	}
}

func (t *tui) confirm(question string, def bool) bool {
	d := "y/N"
	if def {
		d = "Y/n"
	}
	v, ok := t.input(question+" "+dim("("+d+")"), "", nil)
	if !ok {
		return false
	}
	switch strings.ToLower(v) {
	case "":
		return def
	case "y", "yes":
		return true
	}
	return false
}

// page clears the screen and runs fn on a normal terminal, then waits for a
// key so its output can be read.
func (t *tui) page(header, title string, fn func() error) {
	t.cooked()
	fmt.Print(clearScreen() + header)
	fmt.Println(" " + bold(title))
	if err := fn(); err != nil {
		fmt.Println()
		bad("%v", err)
	}
	t.pause()
}

func (t *tui) pause() {
	t.rawMode()
	t.write("\n " + dim("press any key to continue") + " ")
	t.readKey()
	t.write("\n")
}

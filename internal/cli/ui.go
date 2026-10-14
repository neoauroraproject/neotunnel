package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// Minimal styling: colors only on a terminal and only without NO_COLOR.
var color = term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("NO_COLOR") == ""

func paint(code, s string) string {
	if !color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func bold(s string) string   { return paint("1", s) }
func dim(s string) string    { return paint("2", s) }
func accent(s string) string { return paint("38;5;44", s) } // teal
func green(s string) string  { return paint("38;5;78", s) }
func yellow(s string) string { return paint("38;5;221", s) }
func red(s string) string    { return paint("38;5;203", s) }

func success(f string, a ...any) { fmt.Println(green("✓ ") + fmt.Sprintf(f, a...)) }
func warn(f string, a ...any)    { fmt.Println(yellow("! ") + fmt.Sprintf(f, a...)) }
func bad(f string, a ...any)     { fmt.Println(red("✗ ") + fmt.Sprintf(f, a...)) }
func info(f string, a ...any)    { fmt.Println(dim("· ") + fmt.Sprintf(f, a...)) }

func title(s string) {
	fmt.Println()
	fmt.Println(accent("▍") + bold(s))
}

func onOff(up bool) string {
	if up {
		return green("● up")
	}
	return red("● down")
}

var stdin = bufio.NewReader(os.Stdin)

// ask prompts for a line, returning def on empty input.
func ask(prompt, def string) string {
	if def != "" {
		fmt.Printf("%s %s: ", prompt, dim("["+def+"]"))
	} else {
		fmt.Printf("%s: ", prompt)
	}
	line, _ := stdin.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func confirm(prompt string, def bool) bool {
	d := "y/N"
	if def {
		d = "Y/n"
	}
	a := strings.ToLower(ask(prompt+" "+dim("("+d+")"), ""))
	if a == "" {
		return def
	}
	return a == "y" || a == "yes"
}

func bytesHuman(n uint64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(u), 0
	for m := n / u; m >= u; m /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

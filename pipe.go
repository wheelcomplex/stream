// Package pipe provides filters that can be chained together in a manner
// similar to Unix pipelines. Each filter is a function that takes as
// input a sequence of strings (read from a channel) and produces as
// output a sequence of strings (written to a channel).
package pipe

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
)

// Arg contains the data passed to a Filter. The important parts are
// a channel that produces the input to the filter, and a channel
// that receives the output from the filter.  It may be extended
// in the future to contain more fields.
type Arg struct {
	In  <-chan string // In yields the sequence of items that are the input to a Filter.
	Out chan<- string // Out consumes the sequence of items that are the output of a Filter.
	// TODO: add an error channel here?
}

// Filter reads a sequence of strings from a channel and produces a
// sequence on another channel.
type Filter func(Arg)

// ForEach() returns a channel that contains all output emitted by a
// sequence of filters. The empty stream is fed as input to the first filter.
// The output of each filter is fed as input to the next filter. The
// output of the last filter is returned.
func ForEach(filters ...Filter) <-chan string {
	in := make(chan string, 0)
	close(in)
	out := make(chan string, 10000)
	go runAndClose(Sequence(filters...), Arg{in, out})
	return out
}

// Print() prints all items emitted by a sequence of filters, one per
// line. The empty stream is fed as input to the first filter.  The
// output of each filter is fed as input to the next filter. The
// output of the last filter is printed.
func Print(filters ...Filter) {
	for s := range ForEach(filters...) {
		fmt.Println(s)
	}
}

// Sequence returns a filter that is the concatenation of all filter arguments.
// The output of a filter is fed as input to the next filter.
func Sequence(filters ...Filter) Filter {
	return func(arg Arg) {
		in := arg.In
		for _, f := range filters {
			c := make(chan string, 10000)
			go runAndClose(f, Arg{in, c})
			in = c
		}
		passThrough(Arg{in, arg.Out})
	}
}

func runAndClose(f Filter, arg Arg) {
	f(arg)
	close(arg.Out)
}

// passThrough copies all items read from in to out.
func passThrough(arg Arg) {
	for s := range arg.In {
		arg.Out <- s
	}
}

// Echo copies its input and then emits items.
func Echo(items ...string) Filter {
	return func(arg Arg) {
		passThrough(arg)
		for _, s := range items {
			arg.Out <- s
		}
	}
}

// Numbers copies its input and then emits the integers x..y
func Numbers(x, y int) Filter {
	return func(arg Arg) {
		passThrough(arg)
		for i := x; i <= y; i++ {
			arg.Out <- fmt.Sprintf("%d", i)
		}
	}
}

// Cat copies all input and then emits each line from each named file in order.
func Cat(filenames ...string) Filter {
	return func(arg Arg) {
		passThrough(arg)
		for _, f := range filenames {
			file, err := os.Open(f)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				continue
			}
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				arg.Out <- scanner.Text()
			}
			file.Close()
		}
	}
}

// Map calls fn(x) for every item x and yields the outputs of the fn calls.
func Map(fn func(string) string) Filter {
	return func(arg Arg) {
		for s := range arg.In {
			arg.Out <- fn(s)
		}
	}
}

// If emits every input x for which fn(x) is true.
func If(fn func(string) bool) Filter {
	return func(arg Arg) {
		for s := range arg.In {
			if fn(s) {
				arg.Out <- s
			}
		}
	}
}

// Grep emits every input x that matches the regular expression r.
func Grep(r string) Filter {
	re := regexp.MustCompile(r)
	return If(re.MatchString)
}

// GrepNot emits every input x that does not match the regular expression r.
func GrepNot(r string) Filter {
	re := regexp.MustCompile(r)
	return If(func(s string) bool { return !re.MatchString(s) })
}

// Uniq squashes adjacent identical items in arg.In into a single output.
func Uniq() Filter {
	return func(arg Arg) {
		first := true
		last := ""
		for s := range arg.In {
			if first || last != s {
				arg.Out <- s
			}
			last = s
			first = false
		}
	}
}

// UniqWithCount squashes adjacent identical items in arg.In into a single
// output prefixed with the count of identical items.
func UniqWithCount() Filter {
	return func(arg Arg) {
		current := ""
		count := 0
		for s := range arg.In {
			if s != current {
				if count > 0 {
					arg.Out <- fmt.Sprintf("%d %s", count, current)
				}
				count = 0
				current = s
			}
			count++
		}
		if count > 0 {
			arg.Out <- fmt.Sprintf("%d %s", count, current)
		}
	}
}

// Substitute replaces all occurrences of the regular expression r in
// an input item with replacement.  The replacement string can contain
// $1, $2, etc. which represent submatches of r.
func Substitute(r, replacement string) Filter {
	re := regexp.MustCompile(r)
	return func(arg Arg) {
		for s := range arg.In {
			arg.Out <- re.ReplaceAllString(s, replacement)
		}
	}
}

// Reverse yields items in the reverse of the order it received them.
func Reverse() Filter {
	return func(arg Arg) {
		var data []string
		for s := range arg.In {
			data = append(data, s)
		}
		for i := len(data) - 1; i >= 0; i-- {
			arg.Out <- data[i]
		}
	}
}

// First yields the first n items that it receives.
func First(n int) Filter {
	return func(arg Arg) {
		emitted := 0
		for s := range arg.In {
			if emitted < n {
				arg.Out <- s
				emitted++
			}
		}
	}
}

// DropFirst yields all items except for the first n items that it receives.
func DropFirst(n int) Filter {
	return func(arg Arg) {
		emitted := 0
		for s := range arg.In {
			if emitted >= n {
				arg.Out <- s
			}
			emitted++
		}
	}
}

// Last yields the last n items that it receives.
func Last(n int) Filter {
	return func(arg Arg) {
		var buf []string
		for s := range arg.In {
			buf = append(buf, s)
			if len(buf) > n {
				buf = buf[1:]
			}
		}
		for _, s := range buf {
			arg.Out <- s
		}
	}
}

// DropFirst yields all items except for the last n items that it receives.
func DropLast(n int) Filter {
	return func(arg Arg) {
		var buf []string
		for s := range arg.In {
			buf = append(buf, s)
			if len(buf) > n {
				arg.Out <- buf[0]
				buf = buf[1:]
			}
		}
	}
}

// NumberLines prefixes its item with its index in the input sequence
// (starting at 1).
func NumberLines() Filter {
	return func(arg Arg) {
		line := 1
		for s := range arg.In {
			arg.Out <- fmt.Sprintf("%5d %s", line, s)
			line++
		}
	}
}

// Cut emits just the bytes indexed [start..end] of each input item.
func Cut(start, end int) Filter {
	return func(arg Arg) {
		for s := range arg.In {
			if len(s) > end {
				s = s[:end+1]
			}
			if len(s) < start {
				s = ""
			} else {
				s = s[start:]
			}
			arg.Out <- s
		}
	}
}

// Select splits each item into columns and yields the concatenation
// of the columns numbers passed as arguments to Select.  Columns are
// numbered starting at 1. A column number of 0 is interpreted as the
// full string.
func Select(columns ...int) Filter {
	return func(arg Arg) {
		for s := range arg.In {
			result := ""
			for _, col := range columns {
				if e, c := column(s, col); e == 0 && c != "" {
					if result != "" {
						result = result + " "
					}
					result = result + c
				}
			}
			arg.Out <- result
		}
	}
}

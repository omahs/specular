package fmt

import (
	"fmt"
	"io"
)

func Fprintf(w io.Writer, format string, a ...any) (n int, err error) {
	return fmt.Fprintf(w, format, a...)
}

func Printf(format string, a ...any) (n int, err error) {
	return fmt.Printf(format, a...)
}

func Sprintf(format string, a ...any) string {
	return fmt.Sprintf(format, a...)
}

func Fprint(w io.Writer, a ...any) (n int, err error) {
	return fmt.Fprint(w, a...)
}

func Print(a ...any) (n int, err error) {
	return fmt.Print(a...)
}

func Sprint(a ...any) string {
	return fmt.Sprint(a...)
}

func Fprintln(w io.Writer, a ...any) (n int, err error) {
	return fmt.Fprintln(w, a...)
}

func Println(a ...any) (n int, err error) {
	return fmt.Println(a...)
}

func Sprintln(a ...any) string {
	return fmt.Sprintln(a...)
}

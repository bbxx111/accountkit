// testgate verifies that required integration tests actually ran.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type event struct{ Action, Package, Test string }

func verify(r io.Reader, required []string) error {
	pending := map[string]bool{}
	for _, name := range required {
		if name = strings.TrimSpace(name); name != "" {
			pending[name] = true
		}
	}
	if len(pending) == 0 {
		return fmt.Errorf("required test list is empty")
	}
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scan.Scan() {
		var e event
		if err := json.Unmarshal(scan.Bytes(), &e); err != nil {
			return fmt.Errorf("invalid test event: %w", err)
		}
		key := e.Package + "/" + e.Test
		if e.Action == "fail" {
			return fmt.Errorf("test failure: %s", key)
		}
		if e.Action == "skip" && pending[key] {
			return fmt.Errorf("required test skipped: %s", key)
		}
		if e.Action == "pass" {
			delete(pending, key)
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	if len(pending) > 0 {
		return fmt.Errorf("required tests did not pass: %v", pending)
	}
	return nil
}
func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: testgate required-tests.txt < test-events.jsonl")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err == nil {
		err = verify(os.Stdin, strings.Split(string(data), "\n"))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

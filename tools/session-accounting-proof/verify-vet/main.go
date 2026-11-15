// Compare actual vet diagnostics to the retained native baseline without
// changing source cancellation, suppressing diagnostics or relabeling RED1.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type baselineDiagnostic struct {
	File          string `json:"file"`
	Column        int    `json:"column"`
	Function      string `json:"function_signature"`
	FunctionBytes int    `json:"function_bytes"`
	FunctionSHA   string `json:"function_sha256"`
	Expression    string `json:"source_expression_exact"`
	ExpressionSHA string `json:"source_expression_sha256"`
	Message       string `json:"message"`
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func verify() error {
	if len(os.Args) != 6 {
		return fmt.Errorf("baseline, source, stdout, stderr and cost required")
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		return err
	}
	var baseline []baselineDiagnostic
	if err = json.Unmarshal(raw, &baseline); err != nil {
		return err
	}
	raw, err = os.ReadFile(os.Args[5])
	if err != nil {
		return err
	}
	var cost struct {
		Exit int `json:"exit"`
	}
	if err = json.Unmarshal(raw, &cost); err != nil {
		return err
	}
	if cost.Exit != 1 {
		return fmt.Errorf("actual vet exit %d differs from retained RED1", cost.Exit)
	}
	raw, err = os.ReadFile(os.Args[3])
	if err != nil {
		return err
	}
	if len(raw) != 0 {
		return fmt.Errorf("unexpected vet stdout")
	}
	raw, err = os.ReadFile(os.Args[4])
	if err != nil {
		return err
	}
	re := regexp.MustCompile(`^(.+\.go):([0-9]+):([0-9]+): (.+)$`)
	seen := make(map[int]bool)
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		// go vet emits package context headings; keep the original raw stream.
		if strings.HasPrefix(line, "# ") {
			continue
		}
		match := re.FindStringSubmatch(line)
		if match == nil {
			return fmt.Errorf("unparsed vet line: %s", line)
		}
		n, err := strconv.Atoi(match[2])
		if err != nil {
			return err
		}
		column, err := strconv.Atoi(match[3])
		if err != nil {
			return err
		}
		file, err := os.ReadFile(filepath.Join(os.Args[2], match[1]))
		if err != nil {
			return err
		}
		lines := strings.SplitAfter(string(file), "\n")
		if n < 1 || n > len(lines) {
			return fmt.Errorf("invalid vet source location")
		}
		expression := lines[n-1]
		accepted := false
		for i, d := range baseline {
			if seen[i] || d.File != match[1] || d.Column != column || d.Message != match[4] || strings.Count(string(file), d.Function) != 1 {
				continue
			}
			start := strings.Index(string(file), d.Function)
			if start+d.FunctionBytes > len(file) || digest(file[start:start+d.FunctionBytes]) != d.FunctionSHA || expression != d.Expression || digest([]byte(expression)) != d.ExpressionSHA {
				continue
			}
			offset := 0
			for _, s := range lines[:n-1] {
				offset += len(s)
			}
			if offset < start || offset >= start+d.FunctionBytes {
				continue
			}
			seen[i] = true
			accepted = true
			break
		}
		if !accepted {
			return fmt.Errorf("new or changed vet diagnostic: %s", line)
		}
	}
	if len(baseline) != 4 || len(seen) != len(baseline) {
		return fmt.Errorf("vet baseline coverage differs: %d/%d", len(seen), len(baseline))
	}
	fmt.Println("actual vet remains RED1; all four retained function/expression/column/message identities match")
	return nil
}
func main() {
	if err := verify(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

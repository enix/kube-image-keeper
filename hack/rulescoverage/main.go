package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: rulescoverage <rules.yaml> <test.yaml>...")
		fmt.Fprintln(os.Stderr, "Checks that the promtool unit test files evaluate every rule of a Prometheus rule file:",
			"an alert_rule_test for each alert, a promql_expr_test for each recording rule.")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() < 2 {
		flag.Usage()
		os.Exit(2)
	}

	rules, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "rulescoverage:", err)
		os.Exit(1)
	}
	tests := make([]file, 0, flag.NArg()-1)
	for _, path := range flag.Args()[1:] {
		content, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rulescoverage:", err)
			os.Exit(1)
		}
		tests = append(tests, file{name: path, content: content})
	}

	problems, err := check(rules, tests)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rulescoverage:", err)
		os.Exit(1)
	}
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, p)
	}
	if len(problems) > 0 {
		os.Exit(1)
	}
}

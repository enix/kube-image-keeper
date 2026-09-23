package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: valuescheck [values.yaml]")
		fmt.Fprintln(os.Stderr, "Checks that every process block of the chart values repeats the root pod settings",
			"(notes/0008). Default: helm/kube-image-keeper/values.yaml.")
		flag.PrintDefaults()
	}
	flag.Parse()

	path := "helm/kube-image-keeper/values.yaml"
	if flag.NArg() > 0 {
		path = flag.Arg(0)
	}

	values, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "valuescheck:", err)
		os.Exit(1)
	}

	problems, err := check(values)
	if err != nil {
		fmt.Fprintln(os.Stderr, "valuescheck:", err)
		os.Exit(1)
	}
	for _, p := range problems {
		fmt.Fprintf(os.Stderr, "%s: %s\n", path, p)
	}
	if len(problems) > 0 {
		os.Exit(1)
	}
}

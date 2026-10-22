package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	version := flag.String("version", "", "fail unless the notes are for this version (the one semantic-release computes)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: releasenotes [-version <version>] render|changes <release-notes.yaml>")
		fmt.Fprintln(os.Stderr, "Validates the notes of a release, then prints the release body (render)",
			"or the chart's artifacthub.io/changes (changes). See CONTRIBUTING.md, \"Release notes\".")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 2 || (flag.Arg(0) != "render" && flag.Arg(0) != "changes") {
		flag.Usage()
		os.Exit(2)
	}

	if err := run(flag.Arg(0), flag.Arg(1), *version); err != nil {
		fmt.Fprintln(os.Stderr, "releasenotes:", err)
		os.Exit(1)
	}
}

func run(command, path, version string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	notes, err := parse(data)
	if err != nil {
		return fmt.Errorf("%s:\n%w", path, err)
	}
	if err := checkVersion(notes, version); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if command == "render" {
		_, err = os.Stdout.WriteString(render(notes))
		return err
	}
	out, err := changes(notes)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(out)
	return err
}

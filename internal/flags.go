package internal

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

type ArrayFlags []string

func (a *ArrayFlags) String() string {
	return strings.Join(*a, ",")
}

func (a *ArrayFlags) Set(value string) error {
	*a = append(*a, value)
	return nil
}

type RegexpArrayFlags []*regexp.Regexp

func (re *RegexpArrayFlags) String() string {
	s := []string{}
	for _, r := range *re {
		s = append(s, r.String())
	}
	return strings.Join(s, ",")
}

func (re *RegexpArrayFlags) Set(value string) error {
	r, err := regexp.Compile(value)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable parse ignored images regex: %s", err.Error())
		os.Exit(1)
	}
	*re = append(*re, r)
	return nil
}

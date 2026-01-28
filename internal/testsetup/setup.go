package testsetup

import (
	"reflect"
	"regexp"

	"github.com/onsi/gomega/format"
)

func init() {
	format.RegisterCustomFormatter(func(value any) (string, bool) {
		if reflect.TypeOf(value) == reflect.TypeOf(&regexp.Regexp{}) {
			return value.(*regexp.Regexp).String(), true
		}
		return "", false
	})
}

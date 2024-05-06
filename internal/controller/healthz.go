package controller

import (
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

func MakeChecker(check func() error) healthz.Checker {
	return func(req *http.Request) error {
		return check()
	}
}

func Healthz() error {
	return nil
}

func Readyz() error {
	return nil
}

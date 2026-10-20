package clusterid

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ConfigMapName = "kuik-cluster-id"
	Key           = "clusterID"
)

var left, right = [...]string{""}, [...]string{""}

func Generate() string { return "" }

func generate(io.Reader) (string, error) { return "", nil }

func Validate(string) error { panic(errors.ErrUnsupported) }

func Ensure(context.Context, client.Client, string) (string, error) { return "", nil }

type Awaiter struct{ minDelay, maxDelay time.Duration }

func Await(client.Reader, string) *Awaiter { return &Awaiter{} }

func (a *Awaiter) Start(context.Context) error { return nil }

func (a *Awaiter) NeedLeaderElection() bool { return false }

func (a *Awaiter) ID() (string, bool) { return "", false }

func (a *Awaiter) Check(*http.Request) error { return nil }

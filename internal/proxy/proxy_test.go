package proxy

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
)

func Test_parseWwwAuthenticate(t *testing.T) {
	tests := []struct {
		name    string
		realm   string
		service string
		scope   string
	}{
		{
			name:    "Simple header",
			realm:   "https://auth.docker.io/token",
			service: "registry.docker.io",
			scope:   "repository:library/busybox:pull",
		},
		{
			name:    "Multi actions scope",
			realm:   "https://auth.docker.io/token",
			service: "registry.docker.io",
			scope:   "repository:library/busybox:pull,push",
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wwwAuthenticateHeader := fmt.Sprintf("Bearer realm=\"%s\",service=\"%s\",scope=\"%s\"", tt.realm, tt.service, tt.scope)
			wwwAuthenticate := parseWwwAuthenticate(wwwAuthenticateHeader)
			g.Expect(wwwAuthenticate).To(Not(BeNil()))
			g.Expect(wwwAuthenticate).To(HaveKeyWithValue("realm", tt.realm))
			g.Expect(wwwAuthenticate).To(HaveKeyWithValue("service", tt.service))
			g.Expect(wwwAuthenticate).To(HaveKeyWithValue("scope", tt.scope))

			wwwAuthenticateHeaderReversed := fmt.Sprintf("Bearer scope=\"%s\",service=\"%s\",realm=\"%s\"", tt.scope, tt.service, tt.realm)
			wwwAuthenticateReversed := parseWwwAuthenticate(wwwAuthenticateHeaderReversed)
			g.Expect(wwwAuthenticateReversed).To(Equal(wwwAuthenticate))
		})
	}
}

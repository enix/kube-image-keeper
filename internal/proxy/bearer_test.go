package proxy

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
	"github.com/onsi/gomega/gstruct"
)

func TestNewBearer(t *testing.T) {
	tests := []struct {
		name          string
		token         string
		accessToken   string
		firstHttpCode int
		invalidJson   bool
		invalidRealm  bool
		requestNb     int
		wantErr       error
	}{
		{
			name:  "With token only",
			token: "my-token",
		},
		{
			name:        "With access token only",
			accessToken: "my-access-token",
		},
		{
			name:          "First request returns HTTP 200",
			firstHttpCode: http.StatusOK,
			requestNb:     1,
		},
		{
			name:        "Invalid JSON",
			invalidJson: true,
			wantErr:     json.Unmarshal([]byte("invalid json"), t),
		},
		{
			name:         "Invalid realm",
			invalidRealm: true,
			requestNb:    1,
			wantErr: &url.Error{
				Op:  "Get",
				URL: "/token?service=registry.docker.io&scope=repository:samalba/my-app:pull,push",
				Err: errors.New("unsupported protocol scheme \"\""),
			},
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := ghttp.NewGHTTPWithGomega(g)
			server := ghttp.NewServer()
			defer server.Close()

			if tt.firstHttpCode == 0 {
				tt.firstHttpCode = http.StatusUnauthorized
			}

			issuedAt := time.Now().Format(time.RFC3339)
			expiresIn := "3600"
			bearerResponse := gh.RespondWithJSONEncoded(http.StatusOK, &Bearer{
				Token:       tt.token,
				AccessToken: tt.accessToken,
				ExpiresIn:   expiresIn,
				IssuedAt:    issuedAt,
			})

			if tt.invalidJson {
				bearerResponse = gh.RespondWith(http.StatusOK, "invalid json")
			}

			realm := server.URL()
			if tt.invalidRealm {
				realm = ""
			}

			server.AppendHandlers(
				ghttp.CombineHandlers(
					gh.VerifyRequest(http.MethodGet, "/"),
					gh.RespondWith(tt.firstHttpCode, nil, http.Header{
						"Www-Authenticate": []string{
							"Bearer realm=\"" + realm + "/token\",service=\"registry.docker.io\",scope=\"repository:samalba/my-app:pull,push\"",
						},
					}),
				),
				ghttp.CombineHandlers(
					gh.VerifyRequest(http.MethodGet, "/token"),
					bearerResponse,
				),
			)

			bearer, err := NewBearer(server.URL(), "/")

			if tt.firstHttpCode != http.StatusUnauthorized {
				expiresIn = ""
				issuedAt = ""
			}

			if tt.wantErr != nil {
				g.Expect(err).To(Equal(tt.wantErr))
				g.Expect(bearer).To(BeNil())
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(*bearer).To(gstruct.MatchAllFields(gstruct.Fields{
					"Token":        Equal(tt.token),
					"AccessToken":  Equal(tt.accessToken),
					"ExpiresIn":    Equal(expiresIn),
					"IssuedAt":     Equal(issuedAt),
					"RefreshToken": BeEmpty(),
				}))
			}

			if tt.requestNb == 0 {
				tt.requestNb = 2
			}

			g.Expect(server.ReceivedRequests()).Should(HaveLen(tt.requestNb))
		})
	}

	t.Run("Fail on first request", func(t *testing.T) {
		_, err := NewBearer("http://localhost:100000", "/")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("invalid port"))
	})
}

func TestGetToken(t *testing.T) {
	g := NewWithT(t)
	bearer := &Bearer{AccessToken: "my-access-token"}

	t.Run("Token set", func(t *testing.T) {
		g.Expect(bearer.GetToken()).To(Equal(bearer.AccessToken))
	})

	t.Run("Token and AccessToken set", func(t *testing.T) {
		bearer.Token = "my-token"
		g.Expect(bearer.GetToken()).To(Equal(bearer.Token))
	})

	t.Run("AccessToken set", func(t *testing.T) {
		bearer.AccessToken = ""
		g.Expect(bearer.GetToken()).To(Equal(bearer.Token))
	})
}

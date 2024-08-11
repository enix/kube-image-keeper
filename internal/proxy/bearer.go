package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

type Bearer struct {
	Token        string
	AccessToken  string
	ExpiresIn    string
	IssuedAt     string
	RefreshToken string
}

var wwwAuthenticateRegexp = regexp.MustCompile(`(?P<key>\w+)="(?P<value>[^"]+)",?`)

func parseWwwAuthenticate(wwwAuthenticate string) map[string]string {
	challenge := strings.SplitN(wwwAuthenticate, " ", 2)[1]
	parts := wwwAuthenticateRegexp.FindAllStringSubmatch(challenge, -1)

	opts := map[string]string{}
	for _, part := range parts {
		opts[part[1]] = part[2]
	}

	return opts
}

func (b *Bearer) GetToken() string {
	if b.Token != "" {
		return b.Token
	}
	return b.AccessToken
}

func NewBearer(endpoint string, path string) (*Bearer, error) {
	response, err := http.Get(endpoint + path)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		return nil, err
	}

	bearer := Bearer{}
	if response.StatusCode == 401 {
		wwwAuthenticate := parseWwwAuthenticate(response.Header.Get("www-authenticate"))
		url := fmt.Sprintf("%s?service=%s&scope=%s", wwwAuthenticate["realm"], wwwAuthenticate["service"], wwwAuthenticate["scope"])

		response, err := http.Get(url)
		if response != nil && response.Body != nil {
			defer response.Body.Close()
		}
		if err != nil {
			return nil, err
		}

		err = json.NewDecoder(response.Body).Decode(&bearer)
		if err != nil {
			return nil, err
		}
	}

	return &bearer, nil
}

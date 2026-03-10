package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var errInvalidURL = errors.New("invalid URL: must use http or https scheme")

func validateURL(rawURL string) (*url.URL, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, errInvalidURL
	}
	if parsedURL.Host == "" {
		return nil, errors.New("invalid URL: missing host")
	}
	return parsedURL, nil
}

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
	fullURL := endpoint + path
	if _, err := validateURL(fullURL); err != nil {
		return nil, fmt.Errorf("invalid endpoint URL: %w", err)
	}

	// #nosec G107,G704 -- URL is validated above to ensure it uses http/https scheme and has a valid host
	response, err := http.Get(fullURL)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		return nil, err
	}

	bearer := Bearer{}
	if response.StatusCode == 401 {
		wwwAuthenticate := parseWwwAuthenticate(response.Header.Get("www-authenticate"))
		realmURL := fmt.Sprintf("%s?service=%s&scope=%s", wwwAuthenticate["realm"], wwwAuthenticate["service"], wwwAuthenticate["scope"])

		if _, err := validateURL(realmURL); err != nil {
			return nil, fmt.Errorf("invalid realm URL from www-authenticate header: %w", err)
		}

		// #nosec G107 -- URL is validated above to ensure it uses http/https scheme and has a valid host
		response, err := http.Get(realmURL)
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

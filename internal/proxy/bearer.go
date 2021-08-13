package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type Bearer struct {
	Token        string
	AccessToken  string
	ExpiresIn    string
	IssuedAt     string
	RefreshToken string
}

func (b *Bearer) GetToken() string {
	if b.Token != "" {
		return b.Token
	}
	return b.AccessToken
}

func NewBearer(endpoint string, scope string) (*Bearer, error) {
	response, err := http.Get(endpoint + "/v2")
	if err != nil {
		return nil, err
	}

	bearer := Bearer{}
	if response.StatusCode == 401 {
		wwwAuthenticate := parseWwwAuthenticate(response.Header.Get("www-authenticate"))
		url := fmt.Sprintf("%s?service=%s&scope=%s", wwwAuthenticate["realm"], wwwAuthenticate["service"], scope)

		response, err := http.Get(url)
		if err != nil {
			return nil, err
		}

		err = json.NewDecoder(response.Body).Decode(&bearer)
		if err != nil {
			return nil, err
		}

		response.Body.Close()
	}
	return &bearer, nil
}

package securews

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

type Invitation struct {
	Version int    `json:"version"`
	Server  string `json:"server"`
	CA      []byte `json:"ca"`
	Token   string `json:"token"`
}

func EncodeInvitation(server string, ca []byte, token string) (string, error) {
	v := Invitation{Version: 1, Server: strings.TrimRight(server, "/"), CA: ca, Token: token}
	if err := v.validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return "ar1." + base64.RawURLEncoding.EncodeToString(raw), nil
}

func DecodeInvitation(text string) (Invitation, error) {
	if len(text) > 16384 || !strings.HasPrefix(text, "ar1.") {
		return Invitation{}, fmt.Errorf("registration needs a trusted installation code (ar1), not a legacy setup token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(text, "ar1."))
	if err != nil {
		return Invitation{}, fmt.Errorf("invalid installation code")
	}
	var v Invitation
	if err = json.Unmarshal(raw, &v); err != nil {
		return v, err
	}
	return v, v.validate()
}

func (v Invitation) validate() error {
	u, err := url.Parse(v.Server)
	if err != nil || v.Version != 1 || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(v.Token, "st_") || len(v.Token) > 128 {
		return fmt.Errorf("invalid installation identity")
	}
	_, err = Roots(v.CA)
	return err
}

func (c *Credentials) CSR() ([]byte, error) {
	key, err := privateKey(c.PrivateKey)
	if err != nil {
		return nil, err
	}
	return csrForKey(key)
}

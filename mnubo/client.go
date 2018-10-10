package mnubo

import (
	"bytes"
	gzip "compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"time"
)

type CompressionConfig struct {
	Request  bool
	Response bool
}

type Mnubo struct {
	ClientId     string
	ClientSecret string
	ClientToken  string
	Host         string
	AccessToken  AccessToken
	Compression  CompressionConfig
}

type ClientRequest struct {
	authorization   string
	method          string
	path            string
	contentType     string
	payload         []byte
	skipCompression bool
}

type AccessToken struct {
	Value     string `json:"access_token"`
	TokenType string `json:"token_type"`
	ExpiresIn int    `json:"expires_in"`
	ExpiresAt time.Time
	Scope     string `json:"scope"`
	Jti       string `json:"jti"`
}

func (at *AccessToken) hasExpired() bool {
	now := time.Now()
	return at.ExpiresAt.Before(now)
}

func NewClient(id string, secret string, host string) *Mnubo {
	return &Mnubo{
		ClientId:     id,
		ClientSecret: secret,
		Host:         host,
	}
}

func NewClientWithToken(token string, host string) *Mnubo {
	return &Mnubo{
		ClientToken: token,
		Host:        host,
	}
}

func (m *Mnubo) isUsingStaticToken() bool {
	return m.ClientToken != ""
}

func (m *Mnubo) GetAccessToken() (AccessToken, error) {
	return m.GetAccessTokenWithScope("ALL")
}

func (m *Mnubo) GetAccessTokenWithScope(scope string) (AccessToken, error) {
	payload := fmt.Sprintf("grant_type=client_credentials&scope=%s", scope)
	data := []byte(fmt.Sprintf("%s:%s", m.ClientId, m.ClientSecret))

	cr := ClientRequest{
		authorization:   fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString(data)),
		method:          "POST",
		path:            "/oauth/token",
		contentType:     "application/x-www-form-urlencoded",
		skipCompression: true,
		payload:         []byte(payload),
	}
	at := AccessToken{}
	body, err := m.doRequest(cr)
	now := time.Now()

	if err == nil {
		err = json.Unmarshal(body, &at)
		if err != nil {
			return at, fmt.Errorf("unable to unmarshall body %t", err)
		}
		dur, err := time.ParseDuration(fmt.Sprintf("%dms", at.ExpiresIn))
		at.ExpiresAt = now.Add(dur)
		m.AccessToken = at
		return at, err
	}
	return at, err
}

func doGzip(w io.Writer, data []byte) error {
	gw, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return err
	}
	if _, err := gw.Write(data); err != nil {
		return err
	}
	if err := gw.Flush(); err != nil {
		return err
	}
	if err := gw.Close(); err != nil {
		return err
	}
	return nil
}

func doGunzip(w io.Writer, data []byte) error {
	gr, err := gzip.NewReader(bytes.NewBuffer(data))
	defer gr.Close()
	if err != nil {
		return err
	}
	ud, err := ioutil.ReadAll(gr)
	if err != nil {
		return err
	}
	w.Write(ud)
	return nil
}

func (m *Mnubo) doRequest(cr ClientRequest) ([]byte, error) {
	var payload []byte

	if m.Compression.Request && !cr.skipCompression {
		var w bytes.Buffer
		err := doGzip(&w, cr.payload)
		if err != nil {
			return nil, fmt.Errorf("unable to gzip request: %t", err)
		}
		payload = w.Bytes()
	} else {
		payload = cr.payload
	}

	req, err := http.NewRequest(cr.method, m.Host+cr.path, bytes.NewReader(payload))

	req.Header.Add("Content-Type", cr.contentType)
	req.Header.Add("X-MNUBO-SDK", "Go")

	if cr.authorization != "" {
		req.Header.Add("Authorization", cr.authorization)
	}

	if m.Compression.Request {
		req.Header.Add("Content-Encoding", "gzip")
	}

	if m.Compression.Response {
		req.Header.Add("Accept-Encoding", "gzip")
	}

	if err != nil {
		return nil, err
	}

	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("unable to send client request: %t", err)
	}
	defer res.Body.Close()

	var body []byte
	body, err = ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("unable to read response body: %t", err)
	}
	if res.Header.Get("Content-Encoding") == "gzip" {
		var w bytes.Buffer
		err := doGunzip(&w, body)

		if err != nil {
			return nil, fmt.Errorf("unable to gunzip response: %t", err)
		}

		body = w.Bytes()
	}
	if res.StatusCode >= http.StatusOK && res.StatusCode < http.StatusMultipleChoices {
		return body, nil
	}

	return nil, fmt.Errorf("request Error: %s", body)
}

func (m *Mnubo) doRequestWithAuthentication(cr ClientRequest, response interface{}) error {
	if m.isUsingStaticToken() {
		cr.authorization = fmt.Sprintf("Bearer %s", m.ClientToken)
	} else {
		if m.AccessToken.hasExpired() {
			_, err := m.GetAccessToken()

			if err != nil {
				return err
			}
		}
		cr.authorization = fmt.Sprintf("Bearer %s", m.AccessToken.Value)
	}

	data, err := m.doRequest(cr)

	if err != nil {
		return err
	}

	return json.Unmarshal(data, response)
}
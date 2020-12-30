package bittrex

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/rainhq/signalr/v2"
)

type authenticator struct {
	mtx           sync.Mutex
	signalrClient *signalr.Client
	apiKey        string
	apiSecret     string
	authenticated bool
}

func newAuthenticator(signalrClient *signalr.Client, apiKey, apiSecret string) *authenticator {
	return &authenticator{
		signalrClient: signalrClient,
		apiKey:        apiKey,
		apiSecret:     apiSecret,
	}
}

func (a *authenticator) Authenticate(ctx context.Context) error {
	a.mtx.Lock()
	defer a.mtx.Unlock()

	if a.authenticated {
		return nil
	}

	if err := a.authenticate(ctx); err != nil {
		return err
	}

	a.authenticated = true
	return nil
}

func (a *authenticator) Request(ctx context.Context, method, url string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	ts := unixTimestamp()
	contentHash := hash(body)

	req.Header.Set("Api-Key", a.apiKey)
	req.Header.Set("Api-Timestamp", ts)
	req.Header.Set("Api-Content-Hash", contentHash)
	req.Header.Set("Api-Signature", signature([]byte(a.apiSecret), []byte(ts+url+method+contentHash)))

	return req, nil
}

func (a *authenticator) Run(ctx context.Context) error {
	stream := a.signalrClient.Callback(ctx, "authenticationExpiring")
	for {
		if err := stream.Read(); err != nil {
			return err
		}

		if err := a.authenticate(ctx); err != nil {
			return err
		}
	}
}

func (a *authenticator) authenticate(ctx context.Context) error {
	ts := unixTimestamp()
	randomContent := randomString(32)
	signedContent := signature([]byte(a.apiSecret), []byte(ts+randomContent))

	ictx, icancel := context.WithTimeout(ctx, 5*time.Second)
	defer icancel()

	return a.signalrClient.Invoke(ictx, "Authenticate", a.apiKey, ts, randomContent, signedContent).Exec()
}

func unixTimestamp() string {
	return strconv.FormatInt(time.Now().UnixNano()/int64(time.Millisecond), 10)
}

func hash(data []byte) string {
	hash := sha512.New()
	_, _ = hash.Write(data)
	return hex.EncodeToString(hash.Sum(nil))
}

func randomString(size int) string {
	b := make([]byte, base64.URLEncoding.DecodedLen(size))
	_, _ = rand.Read(b)

	return base64.URLEncoding.EncodeToString(b)
}

func signature(apiSecret, data []byte) string {
	hash := hmac.New(sha512.New, apiSecret)
	_, _ = hash.Write(data)
	return hex.EncodeToString(hash.Sum(nil))
}

package api

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/go-resty/resty/v2"
)

type Client struct {
	baseURL  string
	username string
	password string
	http     *resty.Client
}

type subsonicEnvelope struct {
	Response subsonicResponse `json:"subsonic-response"`
}

type subsonicResponse struct {
	Status        string         `json:"status"`
	Error         *subsonicError `json:"error,omitempty"`
	ServerVersion string         `json:"serverVersion,omitempty"`
	AlbumList2    albumList2     `json:"albumList2,omitempty"`
	Album         Album          `json:"album,omitempty"`
	Playlists     playlists      `json:"playlists,omitempty"`
	Playlist      Playlist       `json:"playlist,omitempty"`
}

type subsonicError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func NewClient(baseURL, username, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		http:     resty.New(),
	}
}

func (c *Client) call(ctx context.Context, endpoint string, query map[string]string, out any) error {
	values := url.Values{}
	for k, v := range query {
		values.Set(k, v)
	}
	return c.callValues(ctx, endpoint, values, out)
}

func (c *Client) callValues(ctx context.Context, endpoint string, query url.Values, out any) error {
	tok, salt := token(c.password)
	values := url.Values{}
	values.Set("u", c.username)
	values.Set("t", tok)
	values.Set("s", salt)
	values.Set("v", "1.16.1")
	values.Set("c", "navidrome-bpm-plugin")
	values.Set("f", "json")
	for k, vs := range query {
		for _, v := range vs {
			values.Add(k, v)
		}
	}

	resp, err := c.http.R().
		SetContext(ctx).
		SetQueryString(values.Encode()).
		SetResult(out).
		Get(c.baseURL + "/rest/" + endpoint + ".view")
	if err != nil {
		return err
	}
	if resp.IsError() {
		return fmt.Errorf("%s failed: http %d", endpoint, resp.StatusCode())
	}
	if env, ok := out.(*subsonicEnvelope); ok {
		if env.Response.Status != "ok" {
			if env.Response.Error != nil {
				return fmt.Errorf("%s failed: %s", endpoint, env.Response.Error.Message)
			}
			return fmt.Errorf("%s failed: status %q", endpoint, env.Response.Status)
		}
	}
	return nil
}

func (c *Client) Ping(ctx context.Context) error {
	var env subsonicEnvelope
	return c.call(ctx, "ping", nil, &env)
}

func (c *Client) StartScan(ctx context.Context) error {
	var env subsonicEnvelope
	return c.call(ctx, "startScan", nil, &env)
}
